package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

type googleFlowStub struct {
	flow       db.GoogleSignInFlow
	phase      string
	user       *db.User
	accountErr error
	creations  int
}

func (f *googleFlowStub) CreateGoogleReauthenticationToken(context.Context, string, string) error {
	return nil
}

func (f *googleFlowStub) CreateGoogleSignInFlow(_ context.Context, flow db.GoogleSignInFlow) error {
	f.flow = flow
	f.phase = "pending"
	return nil
}
func (f *googleFlowStub) AdvanceGoogleSignInFlow(_ context.Context, state, from, to string) (db.GoogleSignInFlow, error) {
	if state != f.flow.StateHash || f.phase != from || time.Now().After(f.flow.ExpiresAt) {
		return db.GoogleSignInFlow{}, db.ErrGoogleFlowInvalid
	}
	f.phase = to
	return f.flow, nil
}
func (f *googleFlowStub) FinishGoogleSignInFlow(_ context.Context, state, user, code string) error {
	if state != f.flow.StateHash || f.phase != "exchanging" {
		return db.ErrGoogleFlowInvalid
	}
	f.flow.UserID = user
	f.flow.ErrorCode = code
	f.phase = "ready"
	return nil
}
func (f *googleFlowStub) ConsumeGoogleSignInFlow(_ context.Context, poll string) (*db.GoogleSignInFlow, error) {
	if poll != f.flow.PollHash || f.phase == "consumed" {
		return nil, db.ErrGoogleFlowInvalid
	}
	if f.phase != "ready" {
		return nil, nil
	}
	f.phase = "consumed"
	return &f.flow, nil
}
func (f *googleFlowStub) GoogleUser(name, email, subject string) (*db.User, error) {
	f.creations++
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	f.user = &db.User{ID: "google-user", Name: name, Email: email, Provider: "google", ProviderSubject: subject}
	return f.user, nil
}
func (f *googleFlowStub) GetUserByID(id string) (*db.User, error) { return f.user, nil }

func googlePayload(nonce string) *idtoken.Payload {
	return &idtoken.Payload{Issuer: "https://accounts.google.com", Audience: "client-id", Subject: "google-subject", Expires: time.Now().Add(time.Hour).Unix(), Claims: map[string]any{"email": "user@example.com", "email_verified": true, "name": "Google User", "nonce": nonce}}
}

func TestGoogleIdentityValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*idtoken.Payload)
	}{
		{"issuer", func(p *idtoken.Payload) { p.Issuer = "https://evil.test" }},
		{"audience", func(p *idtoken.Payload) { p.Audience = "another-client" }},
		{"subject", func(p *idtoken.Payload) { p.Subject = "" }},
		{"expiry", func(p *idtoken.Payload) { p.Expires = time.Now().Add(-time.Minute).Unix() }},
		{"unverified", func(p *idtoken.Payload) { p.Claims["email_verified"] = false }},
		{"string verified", func(p *idtoken.Payload) { p.Claims["email_verified"] = "true" }},
		{"email", func(p *idtoken.Payload) { delete(p.Claims, "email") }},
		{"nonce", func(p *idtoken.Payload) { p.Claims["nonce"] = "another-flow" }},
		{"authorized party", func(p *idtoken.Payload) { p.Claims["azp"] = "another-client" }},
	}
	if !validGoogleIdentity(googlePayload("nonce"), "nonce", "client-id") {
		t.Fatal("valid identity rejected")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := googlePayload("nonce")
			tc.change(p)
			if validGoogleIdentity(p, "nonce", "client-id") {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestGoogleSignInRoundTrip(t *testing.T) {
	for _, outcome := range []string{"success", "denied", "conflict", "bad-signature", "bad-nonce", "exchange-error"} {
		t.Run(outcome, func(t *testing.T) {
			store := &googleFlowStub{}
			exchanges := 0
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				exchanges++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("code_verifier") != store.flow.Verifier || r.Form.Get("redirect_uri") != "https://misty.test/v1/auth/google/callback" || r.Form.Get("code") != "google-code" {
					t.Error("token exchange was not bound to PKCE and the configured callback")
				}
				if outcome == "exchange-error" {
					http.Error(w, "token exchange failed", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"unused","token_type":"Bearer","id_token":"signed-token"}`))
			}))
			defer tokenServer.Close()
			if outcome == "conflict" {
				store.accountErr = db.ErrProviderConflict
			}
			sessions := 0
			service := &GoogleSignInService{store: store, startURL: "https://misty.test/v1/auth/google/start", oauth: oauth2.Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://misty.test/v1/auth/google/callback", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: tokenServer.URL, AuthStyle: oauth2.AuthStyleInParams}, Scopes: []string{"openid", "email", "profile"}}, validate: func(_ context.Context, raw, audience string) (*idtoken.Payload, error) {
				if raw != "signed-token" || audience != "client-id" {
					t.Error("wrong ID token validation inputs")
				}
				if outcome == "bad-signature" {
					return nil, errors.New("bad signature")
				}
				p := googlePayload(store.flow.Nonce)
				if outcome == "bad-nonce" {
					p.Claims["nonce"] = "attacker"
				}
				return p, nil
			}, session: func(w http.ResponseWriter, r *http.Request, u *db.User) {
				sessions++
				if u.Provider != "google" {
					t.Error("wrong account")
				}
				w.WriteHeader(200)
			}}
			begin := httptest.NewRecorder()
			service.Begin()(begin, httptest.NewRequest("POST", "/v1/auth/google", strings.NewReader(`{}`)))
			if begin.Code != 201 {
				t.Fatal(begin.Code)
			}
			var flow struct {
				URL   string `json:"url"`
				Token string `json:"flow_token"`
			}
			if err := json.Unmarshal(begin.Body.Bytes(), &flow); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(flow.URL, flow.Token) || flow.Token == "" {
				t.Fatal("poll secret leaked or missing")
			}
			launch, _ := url.Parse(flow.URL)
			state := launch.Query().Get("state")
			poll := func(token string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				body, _ := json.Marshal(map[string]string{"flow_token": token})
				service.Complete()(w, httptest.NewRequest("POST", "/v1/auth/google/complete", strings.NewReader(string(body))))
				return w
			}
			if poll(state).Code != 410 {
				t.Fatal("browser state can redeem a session")
			}
			if poll(flow.Token).Code != 202 {
				t.Fatal("pending flow not pending")
			}
			start := httptest.NewRecorder()
			service.Start()(start, httptest.NewRequest("GET", flow.URL, nil))
			if start.Code != 303 {
				t.Fatal(start.Code)
			}
			destination, _ := url.Parse(start.Header().Get("Location"))
			q := destination.Query()
			if q.Get("state") != state || q.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(store.flow.Verifier) || q.Get("code_challenge_method") != "S256" || q.Get("nonce") != store.flow.Nonce || q.Get("scope") != "openid email profile" {
				t.Fatal("authorization parameters incomplete")
			}
			cookies := start.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("insecure state cookie")
			}
			callbackURL := service.oauth.RedirectURL + "?state=" + state + "&code=google-code"
			if outcome == "denied" {
				callbackURL = service.oauth.RedirectURL + "?state=" + state + "&error=access_denied"
			}
			callback := func(cookie *http.Cookie) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", callbackURL, nil)
				if cookie != nil {
					r.AddCookie(cookie)
				}
				service.Callback()(w, r)
				return w
			}
			if callback(nil).Code != 400 || callback(&http.Cookie{Name: googleSignInCookie, Value: "wrong"}).Code != 400 || exchanges != 0 {
				t.Fatal("callback without browser binding reached Google")
			}
			result := callback(cookies[0])
			want := 400
			if outcome == "success" {
				want = 200
			}
			if result.Code != want {
				t.Fatalf("callback=%d body=%s", result.Code, result.Body.String())
			}
			before := exchanges
			if callback(cookies[0]).Code != 400 || exchanges != before {
				t.Fatal("callback replay accepted")
			}
			complete := poll(flow.Token)
			want = 409
			if outcome == "success" {
				want = 200
			}
			if complete.Code != want {
				t.Fatalf("complete=%d body=%s", complete.Code, complete.Body.String())
			}
			if outcome == "success" && sessions != 1 || outcome != "success" && sessions != 0 {
				t.Fatal("unexpected session issued")
			}
			if outcome != "success" && outcome != "conflict" && store.creations != 0 {
				t.Fatal("invalid identity provisioned")
			}
			if poll(flow.Token).Code != 410 {
				t.Fatal("completion replay accepted")
			}
		})
	}
}

func TestGoogleSignInConfiguration(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
	t.Setenv("MISTY_PUBLIC_API_URL", "https://api.misty.test/v1")
	t.Setenv("GOOGLE_SIGN_IN_REDIRECT_URL", "")
	s, err := NewGoogleSignInService(nil)
	if err != nil || s.oauth.RedirectURL != "https://api.misty.test/v1/auth/google/callback" {
		t.Fatalf("configuration: %v", err)
	}
	for _, value := range []string{"http://evil.test/v1/auth/google/callback", "https://api.test/wrong", "https://user:pass@api.test/auth/google/callback", "https://api.test/auth/google/callback?next=evil"} {
		t.Setenv("GOOGLE_SIGN_IN_REDIRECT_URL", value)
		if _, err := NewGoogleSignInService(nil); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	t.Setenv("GOOGLE_SIGN_IN_REDIRECT_URL", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	s, err = NewGoogleSignInService(nil)
	if err != nil || s.oauth.ClientID != "" {
		t.Fatal("missing credentials enabled sign-in")
	}
}

func TestGoogleReauthenticationRequiresSameIdentity(t *testing.T) {
	store := &googleFlowStub{user: &db.User{ID: "existing", Provider: "google", ProviderSubject: "google-subject"}}
	service := &GoogleSignInService{store: store}
	flow := db.GoogleSignInFlow{ReauthenticateUserID: "existing", ExpiresAt: time.Now().Add(googleSignInTTL)}
	payload := googlePayload("nonce")
	if user, err := service.googleAccount(flow, payload, "Google", "user@example.com"); err != nil || user.ID != "existing" {
		t.Fatal("fresh reauthentication rejected")
	}
	payload.Subject = "another-subject"
	if _, err := service.googleAccount(flow, payload, "Other", "other@example.com"); !errors.Is(err, db.ErrProviderConflict) {
		t.Fatal("different identity accepted")
	}
	if store.creations != 0 {
		t.Fatal("reauthentication creates accounts")
	}
}
