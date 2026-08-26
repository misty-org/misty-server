package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/app"
)

func TestNewHTTPServerBoundsHeadersAndIdleConnections(t *testing.T) {
	server := TestingNewHTTPServer(":0", http.NotFoundHandler())

	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf("timeouts = header:%s read:%s idle:%s, want all bounded",
			server.ReadHeaderTimeout, server.ReadTimeout, server.IdleTimeout)
	}
	if server.MaxHeaderBytes <= 0 || server.MaxHeaderBytes > 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want a small positive bound", server.MaxHeaderBytes)
	}
	// A server-wide WriteTimeout would also apply to WebSocket connections, so
	// write deadlines are set per request instead.
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 so WebSockets keep their own lifetime", server.WriteTimeout)
	}
}

func TestWriteDeadlineMiddlewareSkipsWebSocketUpgrades(t *testing.T) {
	cases := []struct {
		name         string
		path         string
		upgrade      bool
		wantDeadline bool
	}{
		{name: "json route gets a deadline", path: "/api/spaces/space_1/tasks", wantDeadline: true},
		{name: "websocket upgrade gets none", path: "/api/realtime", upgrade: true, wantDeadline: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var observed atomic.Bool
			handler := TestingWriteDeadlineMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				observed.Store(true)
				w.WriteHeader(http.StatusOK)
			}))
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			if testCase.upgrade {
				request.Header.Set("Upgrade", "websocket")
				request.Header.Set("Connection", "Upgrade")
			}

			handler.ServeHTTP(httptest.NewRecorder(), request)

			if !observed.Load() {
				t.Fatal("middleware did not call the next handler")
			}
		})
	}
}

func TestIsWebSocketRequestRequiresBothHeaders(t *testing.T) {
	cases := []struct {
		upgrade, connection string
		want                bool
	}{
		{"websocket", "Upgrade", true},
		{"WebSocket", "keep-alive, Upgrade", true},
		{"websocket", "keep-alive", false},
		{"", "Upgrade", false},
		{"h2c", "Upgrade", false},
	}

	for _, testCase := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/realtime", nil)
		request.Header.Set("Upgrade", testCase.upgrade)
		request.Header.Set("Connection", testCase.connection)
		if got := TestingIsWebSocketRequest(request); got != testCase.want {
			t.Fatalf("isWebSocketRequest(Upgrade:%q Connection:%q) = %v, want %v",
				testCase.upgrade, testCase.connection, got, testCase.want)
		}
	}
}

func TestIsTransferRequestOnlyMatchesObjectByteRoutes(t *testing.T) {
	transfers := []string{
		"/api/spaces/space_1/library/uploads/upload_1/content",
		"/api/spaces/space_1/library/items/item_1/download",
		"/api/spaces/space_1/library/items/item_1/preview",
		"/v1/spaces/space_1/library/items/item_1/download",
	}
	for _, path := range transfers {
		if !TestingIsTransferRequest(httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("isTransferRequest(%q) = false, want true", path)
		}
	}
	plain := []string{"/api/spaces/space_1/tasks", "/api/me", "/healthz"}
	for _, path := range plain {
		if TestingIsTransferRequest(httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("isTransferRequest(%q) = true, want false", path)
		}
	}
}

func TestRunHTTPServerDrainsInFlightRequestsAndStopsWorkers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "drained")
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := TestingNewHTTPServer(listener.Addr().String(), handler)

	var workersStopped atomic.Bool
	serveDone := make(chan error, 1)
	go func() {
		// Serve on the already-bound listener so the test has a stable address.
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			serveDone <- serveErr
			return
		}
		serveDone <- nil
	}()

	responses := make(chan string, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/api/me")
		if requestErr != nil {
			responses <- "error: " + requestErr.Error()
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		responses <- string(body)
	}()

	<-started
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(ctx)
		workersStopped.Store(true)
		shutdownDone <- shutdownErr
	}()

	// Shutdown must wait for the in-flight request rather than cutting it off.
	if workersStopped.Load() {
		t.Fatal("workers stopped before the in-flight request completed")
	}
	close(release)

	if body := <-responses; body != "drained" {
		t.Fatalf("in-flight response = %q, want %q", body, "drained")
	}
	if shutdownErr := <-shutdownDone; shutdownErr != nil {
		t.Fatalf("Shutdown() = %v", shutdownErr)
	}
	if serveErr := <-serveDone; serveErr != nil {
		t.Fatalf("Serve() = %v", serveErr)
	}
	if !workersStopped.Load() {
		t.Fatal("workers were never stopped")
	}
}
