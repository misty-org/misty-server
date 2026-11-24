package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/misty-org/misty-server/internal/app"
)

func corsTestServer(t *testing.T) *Server {
	t.Helper()

	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	return server
}

func TestPreflightAllowsEveryClientHeader(t *testing.T) {
	server := corsTestServer(t)

	clientHeaders := []string{
		"Authorization",
		"Content-Type",
		"X-Misty-Platform",
		"X-Misty-Release-Channel",
		"X-Misty-Library-Upload-Token",
	}

	for _, header := range clientHeaders {
		request := httptest.NewRequest(http.MethodOptions, "/v1/spaces/space_1/library", nil)
		request.Header.Set("Origin", "http://127.0.0.1:5174")
		request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		request.Header.Set("Access-Control-Request-Headers", strings.ToLower(header))
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, request)

		if recorder.Header().Get("Access-Control-Allow-Origin") == "" {
			t.Fatalf("preflight carrying %s returned no Access-Control-Allow-Origin; "+
				"the browser reports this as an opaque load failure", header)
		}
	}
}
