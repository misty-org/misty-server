package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// bearerFixture is one account with one live session, signed the way a sign-in signs it.
type bearerFixture struct {
	database        *db.Database
	email, password string
	userID          string
	access, refresh string
}

const bearerFixturePassword = "correct-horse-battery"

func newBearerFixture(t *testing.T) *bearerFixture {
	t.Helper()
	database := openPresenceTestDatabase(t)
	email := uniqueTestEmail("bearer")
	user, err := database.CreateUser(email, bearerFixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	sid := "sid-" + uniqueTestEmail("bearer")
	expires := time.Now().Add(time.Hour)
	access, err := signer.Mint(user.ID, sid, "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := signer.Mint(user.ID, sid, "refresh", expires)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRefreshSession(context.Background(), security.HashToken(sid), security.HashToken(refresh), user.ID, expires); err != nil {
		t.Fatal(err)
	}
	return &bearerFixture{database: database, email: email, password: bearerFixturePassword, userID: user.ID, access: access, refresh: refresh}
}

func bearerRequest(method, token string) *http.Request {
	r := httptest.NewRequest(method, "/v1/x", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func desktopRequest(token string) *http.Request {
	r := bearerRequest(http.MethodPost, token)
	r.Header.Set(TokenTransportHeader, "bearer")
	return r
}

func noTokenHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{AccessTokenHeader, RefreshTokenHeader} {
		if w.Header().Get(name) != "" {
			t.Fatalf("%s was readable by this client", name)
		}
	}
}

func TestSessionAcceptsAnAccessTokenAsBearer(t *testing.T) {
	f := newBearerFixture(t)
	r := bearerRequest(http.MethodGet, f.access)
	if user, err := accounts.SessionUserID(r); err != nil || user != f.userID {
		t.Fatalf("bearer access = %q %v, want %q", user, err, f.userID)
	}
	if accounts.SessionID(r) == "" {
		t.Fatal("a bearer session has no session id")
	}
}

func TestSessionRefusesEveryOtherBearer(t *testing.T) {
	f := newBearerFixture(t)
	for name, token := range map[string]string{"refresh token": f.refresh, "garbage": "not-a-jwt"} {
		r := bearerRequest(http.MethodGet, token)
		// A cookie doesn't rescue a bearer that failed.
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: f.access})
		if user, err := accounts.SessionUserID(r); err != nil || user != "" {
			t.Fatalf("%s signed in as %q: %v", name, user, err)
		}
		if accounts.SessionID(r) != "" {
			t.Fatalf("%s has a session id", name)
		}
	}
}

func TestBearerRefreshRotatesAndAnswersInHeaders(t *testing.T) {
	f := newBearerFixture(t)
	w := httptest.NewRecorder()
	RefreshSession(f.database)(w, desktopRequest(f.refresh))
	if w.Code != http.StatusNoContent {
		t.Fatalf("refresh = %d %s", w.Code, w.Body.String())
	}
	access, refresh := w.Header().Get(AccessTokenHeader), w.Header().Get(RefreshTokenHeader)
	if access == "" || refresh == "" || refresh == f.refresh {
		t.Fatalf("tokens not rotated into headers: %q %q", access, refresh)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a bearer refresh set cookies")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("tokens were cacheable")
	}
	if user, _ := accounts.SessionUserID(bearerRequest(http.MethodGet, access)); user != f.userID {
		t.Fatal("the rotated access token doesn't sign in")
	}
	next := httptest.NewRecorder()
	RefreshSession(f.database)(next, desktopRequest(refresh))
	if next.Code != http.StatusNoContent || next.Header().Get(RefreshTokenHeader) == "" {
		t.Fatalf("the rotated refresh token was refused: %d", next.Code)
	}
}

func TestRefreshRefusesAnAccessToken(t *testing.T) {
	f := newBearerFixture(t)
	w := httptest.NewRecorder()
	RefreshSession(f.database)(w, desktopRequest(f.access))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("refresh with an access token = %d", w.Code)
	}
	noTokenHeaders(t, w)
	if w.Header().Get(SessionEndedHeader) != "1" {
		t.Fatal("the client wasn't told its session ended")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a bearer client was sent cookies")
	}
}

func TestReusedRefreshTokenIsRefused(t *testing.T) {
	f := newBearerFixture(t)
	first := httptest.NewRecorder()
	RefreshSession(f.database)(first, desktopRequest(f.refresh))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first refresh = %d", first.Code)
	}
	// A retry just after a rotation is forgiven for a moment; past that it's a replay.
	if err := f.database.WithRLSContext(context.Background(), db.ServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE sessions SET refresh_rotated_at=$2 WHERE user_id=$1`,
			f.userID, time.Now().Add(-db.RefreshRotationGrace-time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replay := httptest.NewRecorder()
	RefreshSession(f.database)(replay, desktopRequest(f.refresh))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed refresh = %d", replay.Code)
	}
	noTokenHeaders(t, replay)
	// A replay ends the whole session, so the token it was rotated to is dead too.
	after := httptest.NewRecorder()
	RefreshSession(f.database)(after, desktopRequest(first.Header().Get(RefreshTokenHeader)))
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after a replay = %d", after.Code)
	}
}

func TestCookieRefreshNeverAnswersInHeaders(t *testing.T) {
	f := newBearerFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	r.Header.Set(TokenTransportHeader, "bearer")
	r.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: f.refresh})
	w := httptest.NewRecorder()
	RefreshSession(f.database)(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("cookie refresh = %d", w.Code)
	}
	noTokenHeaders(t, w)
	if len(w.Result().Cookies()) != 2 {
		t.Fatalf("cookie refresh set %d cookies", len(w.Result().Cookies()))
	}
}

func TestBearerRefreshFromAPageStaysInCookies(t *testing.T) {
	f := newBearerFixture(t)
	r := desktopRequest(f.refresh)
	r.Header.Set("Origin", "https://misty.test")
	w := httptest.NewRecorder()
	RefreshSession(f.database)(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("refresh = %d", w.Code)
	}
	noTokenHeaders(t, w)
}

func signInFixture(f *bearerFixture, origin string, transport bool) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": f.email, "password": f.password})
	r := httptest.NewRequest(http.MethodPost, "/v1/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if transport {
		r.Header.Set(TokenTransportHeader, "bearer")
	}
	w := httptest.NewRecorder()
	Login(f.database)(w, r)
	return w
}

func TestSignInWithTheTransportHeaderAnswersInHeaders(t *testing.T) {
	f := newBearerFixture(t)
	w := signInFixture(f, "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get(AccessTokenHeader) == "" || w.Header().Get(RefreshTokenHeader) == "" {
		t.Fatal("no tokens in the headers")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a desktop sign-in set cookies")
	}
	if strings.Contains(w.Body.String(), w.Header().Get(AccessTokenHeader)) {
		t.Fatal("a token reached the body")
	}
}

func TestSignInFromAPageNeverAnswersInHeaders(t *testing.T) {
	f := newBearerFixture(t)
	w := signInFixture(f, "https://misty.test", true)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", w.Code, w.Body.String())
	}
	noTokenHeaders(t, w)
	if len(w.Result().Cookies()) != 2 {
		t.Fatalf("sign-in set %d cookies", len(w.Result().Cookies()))
	}
}

func TestLogoutRevokesABearerSession(t *testing.T) {
	for _, use := range []string{"refresh", "access"} {
		t.Run(use, func(t *testing.T) {
			f := newBearerFixture(t)
			token := f.refresh
			if use == "access" {
				token = f.access
			}
			w := httptest.NewRecorder()
			Logout(f.database)(w, desktopRequest(token))
			if w.Code != http.StatusOK {
				t.Fatalf("logout = %d", w.Code)
			}
			if w.Header().Get(SessionEndedHeader) != "1" || len(w.Result().Cookies()) != 0 {
				t.Fatal("a bearer client should be told in a header, not cookies")
			}
			after := httptest.NewRecorder()
			RefreshSession(f.database)(after, desktopRequest(f.refresh))
			if after.Code != http.StatusUnauthorized {
				t.Fatalf("refresh after logout = %d", after.Code)
			}
		})
	}
}

func TestPasswordRulesAreEnforcedOnTheServer(t *testing.T) {
	for password, ok := range map[string]bool{
		"short-1":                false,
		"ALLCAPS-PASSWORD":       false,
		"nospecialcharacter":     false,
		"lower-and-special":      true,
		"correct horse battery":  true,
		strings.Repeat("a-", 37): false,
	} {
		if err := accounts.ValidateNewPassword(password); (err == nil) != ok {
			t.Fatalf("%q: %v, want accepted=%v", password, err, ok)
		}
	}
}
