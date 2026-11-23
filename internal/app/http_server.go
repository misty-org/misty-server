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

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 60 * time.Second
	httpIdleTimeout       = 120 * time.Second
	httpMaxHeaderBytes    = 64 << 10

	httpJSONWriteTimeout     = 60 * time.Second
	httpTransferWriteTimeout = 10 * time.Minute

	httpShutdownGrace = 20 * time.Second
)

func IsWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func IsTransferRequest(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/v1/") {
		return false
	}
	return strings.Contains(path, "/content") ||
		strings.Contains(path, "/download") ||
		strings.Contains(path, "/preview") ||
		strings.Contains(path, "/thumbnail")
}

func WriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsWebSocketRequest(r) {
			if controller := http.NewResponseController(w); controller != nil {
				_ = controller.SetWriteDeadline(time.Time{})
				_ = controller.SetReadDeadline(time.Time{})
			}
			next.ServeHTTP(w, r)
			return
		}
		timeout := httpJSONWriteTimeout
		if IsTransferRequest(r) {
			timeout = httpTransferWriteTimeout
		}
		if controller := http.NewResponseController(w); controller != nil {
			_ = controller.SetWriteDeadline(time.Now().Add(timeout))
		}
		next.ServeHTTP(w, r)
	})
}

func NewHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           WriteDeadlineMiddleware(handler),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
		ErrorLog:          log.New(os.Stderr, "http: ", log.LstdFlags),
	}
}

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
	stopWorkers()
	if shutdownErr != nil {
		return shutdownErr
	}
	return <-serveErrors
}
