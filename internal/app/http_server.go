package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// HTTP process limits. These are deliberately explicit rather than relying on
// net/http defaults, which impose no timeout at all and leave the process open
// to slow-client resource exhaustion.
const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 60 * time.Second
	httpIdleTimeout       = 120 * time.Second
	httpMaxHeaderBytes    = 64 << 10

	// Write deadline applied per request rather than on the server, so
	// long-lived WebSocket connections are not cut off mid-stream.
	httpJSONWriteTimeout = 60 * time.Second
	// Signed-URL and proxy transfer routes legitimately take longer.
	httpTransferWriteTimeout = 10 * time.Minute

	httpShutdownGrace = 20 * time.Second
)

// streamingRoutePrefixes are paths that hold a connection open far longer than
// a JSON call. They must never inherit the short write deadline.
var streamingRoutePrefixes = []string{"/api/realtime", "/api/spaces"}

// isWebSocketRequest reports whether the client asked to upgrade the
// connection. Upgraded connections take over the socket, so any write deadline
// the HTTP layer set would eventually kill an otherwise healthy session.
func TestingIsWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// isTransferRequest reports whether the request moves object bytes through the
// server. In production with direct R2 transfer this is rare, but the local
// proxy route and server-generated previews still stream.
func TestingIsTransferRequest(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	return strings.Contains(path, "/content") ||
		strings.Contains(path, "/download") ||
		strings.Contains(path, "/preview") ||
		strings.Contains(path, "/thumbnail")
}

// writeDeadlineMiddleware applies a per-request write deadline. WebSocket
// upgrades get none; transfers get a generous one; everything else gets the
// short JSON deadline.
func TestingWriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if TestingIsWebSocketRequest(r) {
			// Clear any inherited deadline so the upgraded socket lives as long
			// as the realtime service's own ping/pong policy allows.
			if controller := http.NewResponseController(w); controller != nil {
				_ = controller.SetWriteDeadline(time.Time{})
				_ = controller.SetReadDeadline(time.Time{})
			}
			next.ServeHTTP(w, r)
			return
		}
		timeout := httpJSONWriteTimeout
		if TestingIsTransferRequest(r) {
			timeout = httpTransferWriteTimeout
		}
		if controller := http.NewResponseController(w); controller != nil {
			_ = controller.SetWriteDeadline(time.Now().Add(timeout))
		}
		next.ServeHTTP(w, r)
	})
}

// newHTTPServer builds the configured server. WriteTimeout is intentionally
// left at zero: the per-request middleware above owns write deadlines so that
// WebSocket connections are not subject to a short one.
func TestingNewHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           TestingWriteDeadlineMiddleware(handler),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
		ErrorLog:          log.New(os.Stderr, "http: ", log.LstdFlags),
	}
}

// runHTTPServer serves until the process receives SIGINT or SIGTERM, then
// drains in-flight requests. stopWorkers is called before the caller shuts down
// the database and realtime services, so background workers cannot touch a
// closed pool.
func runHTTPServer(server *http.Server, stopWorkers context.CancelFunc) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	serveErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
			return
		}
		serveErrors <- nil
	}()

	select {
	case err := <-serveErrors:
		stopWorkers()
		return err
	case received := <-signals:
		log.Printf("received %s, shutting down", received)
	}

	ctx, cancel := context.WithTimeout(context.Background(), httpShutdownGrace)
	defer cancel()
	shutdownErr := server.Shutdown(ctx)
	// Workers stop only after in-flight requests drain, so a request that
	// enqueues work still completes against a live database.
	stopWorkers()
	if shutdownErr != nil {
		return shutdownErr
	}
	return <-serveErrors
}
