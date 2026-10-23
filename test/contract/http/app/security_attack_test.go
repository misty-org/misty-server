package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

// These tests attack the full router the way an outside caller would: weak
// sign-ups, guessing one account's password, reaching another account's Space,
// forging internal calls, cross-site requests and injection payloads.

// attackServer is the full router on the disposable test database that
// .githooks/server-tests.sh provides.
func attackServer(t *testing.T) *Server {
	t.Helper()
	if os.Getenv("TEST_DB_HOST") == "" {
		t.Skip("requires the disposable PostgreSQL from .githooks/server-tests.sh")
	}
	server := noteRouteTestServer(t)
	if err := server.Database.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Database.Conn.Close() })
	return server
}

type attackClient struct {
	t       *testing.T
	server  *Server
	cookies []*http.Cookie
	address string
}

func (c *attackClient) do(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = c.address
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "tauri://localhost")
	request.Header.Set("X-Misty-CSRF", "1")
	for name, value := range headers {
		if value == "" {
			request.Header.Del(name)
		} else {
			request.Header.Set(name, value)
		}
	}
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	c.server.Router.ServeHTTP(recorder, request)
	return recorder
}

// attackRun keeps each run's accounts apart: the test database can outlive a
// run, and an account left by an earlier one would already be registered.
var attackRun = func() string {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	return hex.EncodeToString(suffix)
}()

// attackEmail is the address registerAttackUser gives an account named name.
func attackEmail(name string) string { return name + "_" + attackRun + "@attack.test" }

func registerAttackUser(t *testing.T, server *Server, name, address string) *attackClient {
	t.Helper()
	client := &attackClient{t: t, server: server, address: address}
	response := client.do(http.MethodPost, "/v1/register", map[string]string{
		"name": name, "username": name + "_" + attackRun, "email": attackEmail(name), "password": "correct horse battery staple",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register %s = %d %s", name, response.Code, response.Body.String())
	}
	client.cookies = response.Result().Cookies()
	return client
}

func TestAttackWeakPasswordsAreRefused(t *testing.T) {
	server := attackServer(t)
	client := &attackClient{t: t, server: server, address: "198.51.100.10:4000"}
	for _, password := range []string{"a", "1234567", strings.Repeat("x", 73)} {
		response := client.do(http.MethodPost, "/v1/register", map[string]string{
			"name": "weak", "username": "weak_user", "email": "weak@attack.test", "password": password,
		}, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("weak password %d chars: status %d", len(password), response.Code)
		}
	}
}

func TestAttackPasswordGuessingIsCappedPerAccount(t *testing.T) {
	server := attackServer(t)
	registerAttackUser(t, server, "guess_victim", "198.51.100.20:4000")
	// Guesses spread across addresses, so the per-address limit never trips.
	for attempt := 0; attempt < 10; attempt++ {
		guesser := &attackClient{t: t, server: server, address: "203.0.113." + string(rune('1'+attempt%9)) + ":5000"}
		response := guesser.do(http.MethodPost, "/v1/login", map[string]string{"email": attackEmail("guess_victim"), "password": "wrong guess"}, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: status %d", attempt, response.Code)
		}
	}
	// Even the right password is refused until the window passes.
	late := &attackClient{t: t, server: server, address: "203.0.113.200:5000"}
	response := late.do(http.MethodPost, "/v1/login", map[string]string{"email": strings.ToUpper(attackEmail("guess_victim")), "password": "correct horse battery staple"}, nil)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("eleventh attempt: status %d", response.Code)
	}
	// Another account is unaffected.
	registerAttackUser(t, server, "guess_bystander", "198.51.100.21:4000")
	response = late.do(http.MethodPost, "/v1/login", map[string]string{"email": attackEmail("guess_bystander"), "password": "correct horse battery staple"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("bystander login: status %d %s", response.Code, response.Body.String())
	}
}

func TestAttackAnotherAccountsSpaceIsUnreachable(t *testing.T) {
	server := attackServer(t)
	victim := registerAttackUser(t, server, "space_victim", "198.51.100.30:4000")
	attacker := registerAttackUser(t, server, "space_attacker", "198.51.100.31:4000")
	created := victim.do(http.MethodPost, "/v1/spaces", map[string]string{"name": "Private plans"}, map[string]string{"Idempotency-Key": "attack-space-1"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create space = %d %s", created.Code, created.Body.String())
	}
	listed := victim.do(http.MethodGet, "/v1/spaces", nil, nil)
	var spaces struct {
		Spaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"spaces"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &spaces); err != nil {
		t.Fatal(err)
	}
	spaceID := ""
	for _, space := range spaces.Spaces {
		if space.Name == "Private plans" {
			spaceID = space.ID
		}
	}
	if spaceID == "" {
		t.Fatalf("victim space missing: %s", listed.Body.String())
	}
	escaped := url.PathEscape(spaceID)
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/spaces/" + escaped, nil},
		{http.MethodGet, "/v1/spaces/" + escaped + "/notes", nil},
		{http.MethodPost, "/v1/spaces/" + escaped + "/notes", map[string]string{"title": "planted"}},
		{http.MethodGet, "/v1/spaces/" + escaped + "/members", nil},
		{http.MethodGet, "/v1/spaces/" + escaped + "/messages", nil},
		{http.MethodPost, "/v1/spaces/" + escaped + "/invitations", map[string]string{"email": attackEmail("space_attacker")}},
		{http.MethodDelete, "/v1/spaces/" + escaped, nil},
	} {
		response := attacker.do(probe.method, probe.path, probe.body, map[string]string{"Idempotency-Key": "attack-probe"})
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("attacker %s %s = %d %s", probe.method, probe.path, response.Code, response.Body.String())
		}
	}
	if survived := victim.do(http.MethodGet, "/v1/spaces/"+escaped, nil, nil); survived.Code != http.StatusOK {
		t.Fatalf("victim lost their space: %d", survived.Code)
	}
}

func TestAttackForgedInternalAndCrossSiteCalls(t *testing.T) {
	server := attackServer(t)
	user := registerAttackUser(t, server, "forge_user", "198.51.100.40:4000")
	for _, path := range []string{
		"/internal/agent-runtime/runs/run_00000000-0000-0000-0000-000000000000/events",
		"/internal/agent-runtime/runs/run_00000000-0000-0000-0000-000000000000/complete",
		"/mcp",
	} {
		response := user.do(http.MethodPost, path, map[string]string{"tool": "space.delete"}, map[string]string{
			"X-Misty-Agent-Timestamp": "1", "X-Misty-Agent-Signature": "00", "Idempotency-Key": "forged",
		})
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusServiceUnavailable {
			t.Fatalf("forged %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	// A page on another site holding the user's cookies, with and without the header.
	for _, headers := range []map[string]string{
		{"X-Misty-CSRF": ""},
		{"Origin": "https://attacker.example"},
	} {
		response := user.do(http.MethodPost, "/v1/spaces", map[string]string{"name": "csrf"}, headers)
		if response.Code != http.StatusForbidden {
			t.Fatalf("cross-site write %v = %d", headers, response.Code)
		}
	}
	t.Setenv("MISTY_ENVIRONMENT", "production")
	preflight := user.do(http.MethodOptions, "/v1/spaces", nil, map[string]string{
		"Origin": "http://localhost:5173", "Access-Control-Request-Method": "POST",
	})
	if preflight.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("production granted a credentialed localhost origin")
	}
}

func TestAttackInjectionPayloadsAreInert(t *testing.T) {
	server := attackServer(t)
	user := registerAttackUser(t, server, "inject_user", "198.51.100.50:4000")
	payloads := []string{`' OR '1'='1`, `"; DROP TABLE users; --`, `\x00`, `{{7*7}}`, `<script>alert(1)</script>`, `../../etc/passwd`}
	for _, payload := range payloads {
		for _, path := range []string{
			"/v1/spaces/" + url.PathEscape(payload),
			"/v1/spaces/" + url.PathEscape(payload) + "/notes?q=" + url.QueryEscape(payload),
			"/v1/search/spaces?q=" + url.QueryEscape(payload),
		} {
			response := user.do(http.MethodGet, path, nil, nil)
			if response.Code >= 500 {
				t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
			}
		}
		response := user.do(http.MethodPost, "/v1/login", map[string]string{"email": payload, "password": payload}, nil)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusBadRequest {
			t.Fatalf("login payload %q = %d", payload, response.Code)
		}
	}
	// The accounts table still answers normally.
	response := user.do(http.MethodPost, "/v1/login", map[string]string{"email": attackEmail("inject_user"), "password": "correct horse battery staple"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login after payloads = %d %s", response.Code, response.Body.String())
	}
}
