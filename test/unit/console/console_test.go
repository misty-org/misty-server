package console

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/console"
)

const launchToken = "launch-token"

func newConsole(t *testing.T, apiURL string) http.Handler {
	t.Helper()
	return newConsoleWith(t, Config{Addr: "127.0.0.1:0", APIURL: apiURL, Environment: "development"})
}

func newConsoleWith(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	return NewServer(cfg, nil, launchToken).Handler()
}

func request(t *testing.T, handler http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "127.0.0.1:7070"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func signIn(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	rec := request(t, handler, "/overview?token="+launchToken)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/overview" {
		t.Fatalf("launch link: got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("expected one HttpOnly SameSite=Strict session cookie, got %+v", cookies)
	}
	return cookies[0]
}

func TestValidateLoopbackRejectsRoutableAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:7070", "localhost:7070", "[::1]:7070"} {
		if err := ValidateLoopback(addr); err != nil {
			t.Errorf("%s: unexpected error %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:7070", ":7070", "192.168.1.5:7070", "example.com:7070"} {
		if err := ValidateLoopback(addr); err == nil {
			t.Errorf("%s: expected rejection", addr)
		}
	}
}

func TestRejectsNonLoopbackHostHeader(t *testing.T) {
	handler := newConsole(t, "http://127.0.0.1:1")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "attacker.example:7070"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for rebinding host, got %d", rec.Code)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	rec := request(t, newConsole(t, "http://127.0.0.1:1"), "/healthz")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestPagesRequireSession(t *testing.T) {
	handler := newConsole(t, "http://127.0.0.1:1")
	if rec := request(t, handler, "/overview"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: expected 401, got %d", rec.Code)
	}
	if rec := request(t, handler, "/overview?token=wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: expected 401, got %d", rec.Code)
	}
	forged := &http.Cookie{Name: "misty_console_session", Value: "forged"}
	if rec := request(t, handler, "/overview", forged); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie: expected 401, got %d", rec.Code)
	}
}

func TestOverviewShowsServerHealthAndMetrics(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"degraded","version":"v1.2.3","uptime_seconds":7500,
				"checks":{"database":{"status":"ok","critical":true,"duration_ms":4},
				"collaboration":{"status":"degraded","message":"tunnel reconnecting"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	handler := newConsole(t, api.URL)
	cookie := signIn(t, handler)
	rec := request(t, handler, "/overview", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview: got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Degraded", "v1.2.3", "up 2h 5m", "Collaboration", "tunnel reconnecting", "MISTY_METRICS_TOKEN", "Database (console connection)"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview is missing %q", want)
		}
	}
	if !strings.Contains(body, `hx-headers="{&#34;X-CSRF-Token&#34;:`) {
		t.Error("layout does not attach the CSRF header to htmx requests")
	}
}

func TestUnknownPagesRedirectToOverview(t *testing.T) {
	handler := newConsole(t, "http://127.0.0.1:1")
	cookie := signIn(t, handler)
	rec := request(t, handler, "/bootstrap", cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/overview" {
		t.Fatalf("unknown page: got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestEveryPageRendersWithoutDatabase(t *testing.T) {
	handler := newConsoleWith(t, Config{APIURL: "http://127.0.0.1:1", Environment: "development"})
	cookie := signIn(t, handler)
	for _, page := range []string{"overview", "services", "accounts", "sync", "jobs", "ai", "billing", "configuration", "database"} {
		rec := request(t, handler, "/"+page, cookie)
		if rec.Code != http.StatusOK {
			t.Errorf("/%s: got %d", page, rec.Code)
		}
	}
}

func TestEnvironmentGating(t *testing.T) {
	production := newConsoleWith(t, Config{APIURL: "http://127.0.0.1:1", Environment: "production"})
	cookie := signIn(t, production)
	if rec := request(t, production, "/services/panel", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("services outside development: expected 404, got %d", rec.Code)
	}
	if rec := request(t, production, "/billing", cookie); rec.Code != http.StatusOK {
		t.Errorf("billing in production: expected 200, got %d", rec.Code)
	}
}

func TestMutationsRequireCSRFToken(t *testing.T) {
	handler := newConsole(t, "http://127.0.0.1:1")
	cookie := signIn(t, handler)
	for _, headers := range []map[string]string{
		{},
		{"HX-Request": "true"},
		{"HX-Request": "true", "X-CSRF-Token": "wrong"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/accounts/user_1/revoke-sessions", nil)
		req.Host = "127.0.0.1:7070"
		req.AddCookie(cookie)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("headers %v: expected 403, got %d", headers, rec.Code)
		}
	}
}
