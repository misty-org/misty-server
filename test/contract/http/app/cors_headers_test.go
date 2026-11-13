package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/misty-org/misty-server/internal/app"
)

// corsTestServer builds a server with the minimum valid configuration. Note
// collaboration validates its signing key at startup, so it must be a real one.
func corsTestServer(t *testing.T) *Server {
	t.Helper()
	configureJournalCollabForTest(t)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JOURNAL_COLLAB_TICKET_PRIVATE_KEY", base64.StdEncoding.EncodeToString(pkcs8))
	t.Setenv("JOURNAL_COLLAB_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	t.Setenv("JOURNAL_COLLAB_PROJECTION_SECRET", base64.StdEncoding.EncodeToString(secret))
	t.Setenv("JOURNAL_COLLAB_ROOM_SALT", base64.StdEncoding.EncodeToString(secret))

	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	return server
}

// Every custom header the desktop client sends must survive the preflight.
//
// A missing entry fails in a uniquely unhelpful way: the preflight returns 200
// with no Access-Control-Allow-Origin, so the browser reports an opaque "load
// failed" that points at the origin rather than the header.
func TestPreflightAllowsEveryClientHeader(t *testing.T) {
	server := corsTestServer(t)

	// Kept in sync with the headers the desktop actually sets.
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
