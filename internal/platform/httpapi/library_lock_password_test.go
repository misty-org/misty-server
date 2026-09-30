package api

import (
	"context"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type libraryPasswordStub struct {
	configured bool
	setCalls   int
	userID     string
}

func (s *libraryPasswordStub) LibraryPasswordConfigured(_ context.Context, id string) (bool, error) {
	s.userID = id
	return s.configured, nil
}
func (s *libraryPasswordStub) SetInitialLibraryPassword(_ context.Context, id, password string) error {
	s.setCalls++
	s.userID = id
	if s.configured {
		return db.ErrLibraryPasswordAlreadySet
	}
	s.configured = true
	return nil
}

func TestLibraryPasswordSetupRequiresAccountAndConfirmation(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_AUTH_SIGNING_KEY", "")
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", "")
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint("account", "session", "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	store := &libraryPasswordStub{}
	request := func(method, body string, signedIn bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/me/library-lock", strings.NewReader(body))
		if signedIn {
			r.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		LibraryLockPassword(store)(w, r)
		return w
	}
	if w := request("GET", "", false); w.Code != 401 {
		t.Fatalf("anonymous status=%d", w.Code)
	}
	if w := request("POST", `{"password":"library-password","confirmation":"library-password"}`, false); w.Code != 401 || store.setCalls != 0 {
		t.Fatal("anonymous setup accepted")
	}
	if w := request("GET", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal("first-use status")
	}
	if w := request("POST", `{"password":"library-password","confirmation":"different"}`, true); w.Code != 400 || store.setCalls != 0 {
		t.Fatal("mismatched confirmation accepted")
	}
	if w := request("POST", `{"password":"library-password","confirmation":"library-password","user_id":"other"}`, true); w.Code != 400 || store.setCalls != 0 {
		t.Fatal("body chose account")
	}
	if w := request("POST", `{"password":"library-password","confirmation":"library-password"}`, true); w.Code != 201 || store.userID != "account" {
		t.Fatal("setup failed")
	}
	if w := request("GET", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatal("configured status")
	}
	if w := request("POST", `{"password":"replacement","confirmation":"replacement"}`, true); w.Code != 409 {
		t.Fatal("existing password replaced")
	}
}
