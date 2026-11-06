package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/test/testkit"
)

// confirmationServer mounts sign-up and sign-in on a server that already has an
// account, so nobody signing up here is its owner.
func confirmationServer(t *testing.T) (http.Handler, *db.Database) {
	t.Helper()
	t.Setenv("MISTY_PUBLIC_API_URL", "https://cloud.misty.example/v1")
	t.Setenv("MISTY_HOSTNAME", "cloud.misty.example")
	t.Setenv("MISTY_WEBSITE_URL", "https://site.example")
	t.Setenv("MISTY_OPEN_SIGNUP", "1")
	database := testkit.OpenDatabase(t)
	if _, err := database.CreateUser("owner-"+uuid.NewString()+"@example.com", strongPassword); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/v1/register", Register(database))
	router.Post("/v1/login", Login(database))
	router.Post("/v1/auth/confirm-email", ConfirmEmail(database))
	router.Post("/v1/auth/confirm-email/sign-in", ConfirmEmailSignIn(database))
	return router, database
}

const strongPassword = "correct horse battery staple"

func newPerson() (username, address string) {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	return "p_" + id, "p-" + id + "@example.com"
}

func signUp(t *testing.T, handler http.Handler, username, address, password string) *httptest.ResponseRecorder {
	t.Helper()
	rec, _ := call(t, handler, http.MethodPost, "/v1/register", "",
		`{"username":"`+username+`","email":"`+address+`","password":"`+password+`"}`)
	return rec
}

func passwordSignIn(t *testing.T, handler http.Handler, address, password string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return call(t, handler, http.MethodPost, "/v1/login", "", `{"email":"`+address+`","password":"`+password+`"}`)
}

// confirm presses the button on the website's page and says what it was told.
func confirm(t *testing.T, handler http.Handler, token string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/confirm-email", strings.NewReader(url.Values{"token": {token}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d", rec.Code)
	}
	target, ok := strings.CutPrefix(rec.Header().Get("Location"), "https://site.example/confirm-email?result=")
	if !ok {
		t.Fatalf("confirm went to %q", rec.Header().Get("Location"))
	}
	return target
}

// sentToken reads the token out of the link in the newest email to an address.
func sentToken(t *testing.T, database *db.Database, address string) string {
	t.Helper()
	var link string
	err := database.Conn.QueryRowContext(context.Background(), `SELECT link FROM email_outbox WHERE kind='confirm_email' AND recipient=$1 AND state='pending'`, address).Scan(&link)
	if err != nil {
		t.Fatal(err)
	}
	wanted := "https://site.example/confirm-email#server=cloud.misty.example&token="
	token, ok := strings.CutPrefix(link, wanted)
	if !ok || token == "" {
		t.Fatalf("link = %q", link)
	}
	return token
}

func allowNextEmail(t *testing.T, database *db.Database, address string) {
	t.Helper()
	if _, err := database.Conn.ExecContext(context.Background(), `UPDATE pending_accounts SET sent_at=now()-interval '1 hour' WHERE email=$1`, address); err != nil {
		t.Fatal(err)
	}
}

func TestSignUpMakesNoAccountUntilTheEmailIsConfirmed(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()

	if rec := signUp(t, handler, username, address, strongPassword); rec.Code != http.StatusAccepted || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("sign-up = %d, cookie %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	if user, _, err := database.GetUserByEmail(address); err != nil || user != nil {
		t.Fatalf("account before confirming = %v, %v", user, err)
	}
	if rec, _ := passwordSignIn(t, handler, address, "the wrong password here"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	if rec, body := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusForbidden || body["code"] != "email_not_confirmed" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("sign-in before confirming = %d %v", rec.Code, body)
	}

	token := sentToken(t, database, address)
	if result := confirm(t, handler, token); result != "confirmed" {
		t.Fatalf("confirm = %q", result)
	}
	if rec, body := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusOK || body["username"] != username {
		t.Fatalf("sign-in after confirming = %d %v", rec.Code, body)
	}
	user, _, err := database.GetUserByEmail(address)
	if err != nil || user == nil {
		t.Fatalf("account = %v, %v", user, err)
	}
	// Open signup holds the account to nothing, as it does without confirmation.
	limits, err := database.AccountLimitsFor(context.Background(), user.ID)
	if err != nil || limits.Devices == nil || *limits.Devices != 0 {
		t.Fatalf("limits = %+v, %v", limits, err)
	}
	if result := confirm(t, handler, token); result != "expired" {
		t.Fatalf("second confirm = %q", result)
	}
	if rec := signUp(t, handler, username+"2", address, strongPassword); rec.Code != http.StatusConflict {
		t.Fatalf("sign-up with a taken email = %d", rec.Code)
	}
}

func TestAStrangersSignUpNeverHoldsAnEmail(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	stranger, address := newPerson()
	owner, _ := newPerson()

	signUp(t, handler, stranger, address, "the strangers password")
	first := sentToken(t, database, address)

	// A second sign-up right away sends nothing and changes nothing.
	if rec := signUp(t, handler, owner, address, strongPassword); rec.Code != http.StatusAccepted {
		t.Fatalf("repeat sign-up = %d", rec.Code)
	}
	if sentToken(t, database, address) != first {
		t.Fatal("a repeat within the wait sent another email")
	}

	// Later, the owner's sign-up replaces the stranger's, password and link alike.
	allowNextEmail(t, database, address)
	if rec := signUp(t, handler, owner, address, strongPassword); rec.Code != http.StatusAccepted {
		t.Fatalf("owner's sign-up = %d", rec.Code)
	}
	if result := confirm(t, handler, first); result != "expired" {
		t.Fatalf("the replaced link = %q", result)
	}
	if result := confirm(t, handler, sentToken(t, database, address)); result != "confirmed" {
		t.Fatalf("confirm = %q", result)
	}
	if rec, _ := passwordSignIn(t, handler, address, "the strangers password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the stranger's password = %d", rec.Code)
	}
	if rec, body := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusOK || body["username"] != owner {
		t.Fatalf("the owner's password = %d %v", rec.Code, body)
	}
}

func TestGoogleAccountOutranksAWaitingSignUp(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()

	signUp(t, handler, username, address, strongPassword)
	google, err := database.GoogleUser(address, "subject-"+uuid.NewString())
	if err != nil || google.Provider != "google" {
		t.Fatalf("google = %v, %v", google, err)
	}
	if result := confirm(t, handler, sentToken(t, database, address)); result != "exists" {
		t.Fatalf("confirm = %q", result)
	}
	if rec, body := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusConflict || body["code"] != "use_google" {
		t.Fatalf("the waiting sign-up's password = %d %v", rec.Code, body)
	}
}

func TestConfirmingKeepsAnAccountWhenItsUsernameWasTaken(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()

	signUp(t, handler, username, address, strongPassword)
	if _, err := database.CreateUserWithUsername(username, "other-"+address, strongPassword); err != nil {
		t.Fatal(err)
	}
	if result := confirm(t, handler, sentToken(t, database, address)); result != "confirmed" {
		t.Fatalf("confirm = %q", result)
	}
	if rec, body := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusOK || !strings.HasPrefix(body["username"].(string), username[:10]) || body["username"] == username {
		t.Fatalf("sign-in = %d %v", rec.Code, body)
	}
}

func TestConfirmingOnTheWebsiteSignsIn(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()
	signUp(t, handler, username, address, strongPassword)
	token := sentToken(t, database, address)

	rec, body := call(t, handler, http.MethodPost, "/v1/auth/confirm-email/sign-in", "", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK || body["username"] != username || rec.Header().Get("Set-Cookie") == "" {
		t.Fatalf("confirm = %d %v, cookie %q", rec.Code, body, rec.Header().Get("Set-Cookie"))
	}
	again, body := call(t, handler, http.MethodPost, "/v1/auth/confirm-email/sign-in", "", `{"token":"`+token+`"}`)
	if again.Code != http.StatusBadRequest || body["code"] != "expired" || again.Header().Get("Set-Cookie") != "" {
		t.Fatalf("a used link = %d %v, cookie %q", again.Code, body, again.Header().Get("Set-Cookie"))
	}
}

func TestConfirmingSignsNobodyIntoAnAccountTheEmailGainedMeanwhile(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()
	signUp(t, handler, username, address, strongPassword)
	token := sentToken(t, database, address)
	if _, err := database.CreateUser(address, strongPassword); err != nil {
		t.Fatal(err)
	}

	rec, body := call(t, handler, http.MethodPost, "/v1/auth/confirm-email/sign-in", "", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusConflict || body["code"] != "exists" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("confirm = %d %v, cookie %q", rec.Code, body, rec.Header().Get("Set-Cookie"))
	}
}

func TestAnAppWaitingForConfirmationOpensNoSessionAndKeepsTheLink(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()
	signUp(t, handler, username, address, strongPassword)
	token := sentToken(t, database, address)
	allowNextEmail(t, database, address)
	wait := func(password string) (*httptest.ResponseRecorder, map[string]any) {
		return call(t, handler, http.MethodPost, "/v1/login", "", `{"email":"`+address+`","password":"`+password+`","waiting":true}`)
	}

	if rec, body := wait(strongPassword); rec.Code != http.StatusAccepted || body["status"] != "confirm_email" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("before confirming = %d %v", rec.Code, body)
	}
	if sentToken(t, database, address) != token {
		t.Fatal("waiting sent a new link, which stops the one in the inbox")
	}
	if rec, _ := wait("the wrong password here"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	if result := confirm(t, handler, token); result != "confirmed" {
		t.Fatalf("confirm = %q", result)
	}
	if rec, body := wait(strongPassword); rec.Code != http.StatusOK || body["status"] != "confirmed" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("after confirming = %d %v, cookie %q", rec.Code, body, rec.Header().Get("Set-Cookie"))
	}
	if rec, _ := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusOK || rec.Header().Get("Set-Cookie") == "" {
		t.Fatalf("sign-in after waiting = %d", rec.Code)
	}
}

func TestSignInSendsAWaitingSignUpANewLink(t *testing.T) {
	mailjetConfigured(t)
	handler, database := confirmationServer(t)
	username, address := newPerson()

	signUp(t, handler, username, address, strongPassword)
	first := sentToken(t, database, address)
	if rec, _ := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusForbidden {
		t.Fatalf("sign-in = %d", rec.Code)
	}
	if sentToken(t, database, address) != first {
		t.Fatal("a sign-in right after the first email sent another")
	}
	allowNextEmail(t, database, address)
	if rec, _ := passwordSignIn(t, handler, address, "the wrong password here"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	if sentToken(t, database, address) != first {
		t.Fatal("a wrong password sent a new link")
	}
	if rec, _ := passwordSignIn(t, handler, address, strongPassword); rec.Code != http.StatusForbidden {
		t.Fatalf("sign-in = %d", rec.Code)
	}
	second := sentToken(t, database, address)
	if second == first {
		t.Fatal("no new link")
	}
	if result := confirm(t, handler, first); result != "expired" {
		t.Fatalf("the old link = %q", result)
	}
	if result := confirm(t, handler, second); result != "confirmed" {
		t.Fatalf("the new link = %q", result)
	}
}

func TestWithoutEmailSignUpMakesTheAccountAtOnce(t *testing.T) {
	handler, database := confirmationServer(t)
	username, address := newPerson()

	if rec := signUp(t, handler, username, address, strongPassword); rec.Code != http.StatusCreated || rec.Header().Get("Set-Cookie") == "" {
		t.Fatalf("sign-up = %d", rec.Code)
	}
	if user, _, err := database.GetUserByEmail(address); err != nil || user == nil {
		t.Fatalf("account = %v, %v", user, err)
	}
}

func TestAPasswordForAGoogleEmailSaysToUseGoogle(t *testing.T) {
	handler, database := confirmationServer(t)
	username, address := newPerson()
	if _, err := database.GoogleUser(address, "subject-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	rec, body := passwordSignIn(t, handler, address, strongPassword)
	if rec.Code != http.StatusConflict || body["code"] != "use_google" || !strings.Contains(body["message"].(string), "Continue with Google") || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("sign-in = %d %v", rec.Code, body)
	}
	up, _ := call(t, handler, http.MethodPost, "/v1/register", "", `{"username":"`+username+`","email":"`+address+`","password":"`+strongPassword+`"}`)
	if up.Code != http.StatusConflict || !strings.Contains(up.Body.String(), "use_google") {
		t.Fatalf("sign-up = %d %s", up.Code, up.Body)
	}
	// An email nobody has still answers like a wrong password.
	if rec, _ := passwordSignIn(t, handler, "nobody-"+address, strongPassword); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown email = %d", rec.Code)
	}
}
