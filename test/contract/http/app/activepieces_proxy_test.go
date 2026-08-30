package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

func TestActivepiecesProxyStripsPrefixBeforeForwarding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("resource") != "mcp" {
			t.Fatalf("query = %q", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	defer upstream.Close()

	proxy, err := TestingNewActivepiecesProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/activepieces/.well-known/oauth-authorization-server?resource=mcp", nil)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "proxied" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestActivepiecesProxyRewritesRootDiscoveryPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-protected-resource/mcp" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	proxy, err := TestingNewActivepiecesProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/activepieces/mcp", nil)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestActivepiecesProxyRebasesFrontendAssets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<link href="/style.css"><script src="/app.js"></script>`)
	}))
	defer upstream.Close()

	proxy, err := TestingNewActivepiecesProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/activepieces/mcp-authorize", nil))
	want := `<link href="/activepieces/style.css"><script src="/activepieces/app.js"></script>`
	if response.Body.String() != want {
		t.Fatalf("body = %q, want %q", response.Body.String(), want)
	}
}

func TestActivepiecesProxyRejectsHTTPSOrMalformedTargets(t *testing.T) {
	for _, target := range []string{"https://activepieces.example.com", "http://", "http://user:secret@activepieces-app:80"} {
		t.Run(fmt.Sprintf("%q", target), func(t *testing.T) {
			if _, err := TestingNewActivepiecesProxy(target); err == nil {
				t.Fatalf("target %q was accepted", target)
			}
		})
	}
}
