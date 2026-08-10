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

	. "github.com/kannachi323/misty/server/internal/app"
)

// corsTestServer builds a server with the minimum valid configuration. Note
// collaboration validates its signing key at startup, so it must be a real one.
func corsTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")

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
// failed" that points at the origin rather than the header. Omitting
// X-Misty-Library-Reauthentication made Recently Deleted and Hidden completely
// unreachable, because only those collections send it.
func TestPreflightAllowsEveryClientHeader(t *testing.T) {
	server := corsTestServer(t)

	// Kept in sync with the headers the desktop actually sets.
	clientHeaders := []string{
		"Authorization",
		"Content-Type",
		"X-Misty-Platform",
		"X-Misty-Release-Channel",
		"X-Misty-Session-Id",
		"X-Misty-Analytics-Enabled",
		"X-Misty-Device-Timestamp",
		"X-Misty-Device-Nonce",
		"X-Misty-Device-Signature",
		"X-Misty-Attachment-Upload-Token",
		"X-Misty-Library-Upload-Token",
		"X-Misty-Library-Reauthentication",
	}

	for _, header := range clientHeaders {
		request := httptest.NewRequest(http.MethodOptions, "/api/spaces/space_1/library", nil)
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

// The sensitive-collection request is the one that actually broke, so it gets
// its own case with the exact header combination the client sends.
func TestPreflightAllowsSensitiveCollectionRequest(t *testing.T) {
	server := corsTestServer(t)

	request := httptest.NewRequest(http.MethodOptions,
		"/api/spaces/space_1/library?collection=recently-deleted", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5174")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers",
		"authorization,x-misty-library-reauthentication")
	recorder := httptest.NewRecorder()
	server.Router.ServeHTTP(recorder, request)

	if recorder.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5174" {
		t.Fatalf("Recently Deleted preflight = %q, want the origin echoed back",
			recorder.Header().Get("Access-Control-Allow-Origin"))
	}
	allowed := strings.ToLower(recorder.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowed, "x-misty-library-reauthentication") {
		t.Fatalf("Access-Control-Allow-Headers = %q, missing the reauthentication header", allowed)
	}
}
