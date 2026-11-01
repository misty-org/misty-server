package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"github.com/kannachi323/misty/server/test/testkit"
)

const operatorToken = "operator-test-token"

// issuedKey is the setup key an operator answer carries, failing the test
// when it carries none.
func issuedKey(t *testing.T, body map[string]any) string {
	t.Helper()
	key, _ := accountField(body, "setup_key").(string)
	if key == "" {
		t.Fatalf("no setup key was issued: %v", body)
	}
	return key
}

// operatorServer mounts the routes the app mounts for this feature.
func operatorServer(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv("MISTY_PUBLIC_API_URL", "https://cloud.misty.example/v1")
	t.Setenv("MISTY_HOSTNAME", "cloud.misty.example")
	database := testkit.OpenDatabase(t)
	guard := NewSuspensionGuard(database)
	router := chi.NewRouter()
	router.Use(guard.Middleware)
	router.Post("/v1/login", Login(database))
	router.Post("/v1/setup-keys/redeem", RedeemSetupKey(database))
	router.Post("/v1/auth/refresh", RefreshSession(database))
	router.Get("/v1/me", GetMe(database))
	router.Put("/v1/me/password", ChangePassword(database))
	router.Route("/operator/v1", func(r chi.Router) {
		r.Use(RequireOperator(operatorToken))
		NewOperatorAccounts(database, guard).Mount(r)
	})
	if _, err := database.CreateUser("owner@example.com", "the owner's password"); err != nil {
		t.Fatal(err)
	}
	return router
}

func call(t *testing.T, handler http.Handler, method, path, token, body string, cookies ...*http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func accountField(body map[string]any, path ...string) any {
	var value any = body["account"]
	for _, key := range path {
		object, _ := value.(map[string]any)
		value = object[key]
	}
	return value
}

func TestOperatorAPIRequiresTheOperatorToken(t *testing.T) {
	handler := operatorServer(t)
	for _, token := range []string{"", "wrong", operatorToken + "x"} {
		if rec, body := call(t, handler, http.MethodGet, "/operator/v1/accounts", token, ""); rec.Code != http.StatusUnauthorized || body["code"] != "operator_unauthorized" {
			t.Fatalf("token %q = %d %v", token, rec.Code, body)
		}
	}
	if rec, _ := call(t, handler, http.MethodGet, "/operator/v1/accounts", operatorToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("operator token = %d", rec.Code)
	}
}

func TestOperatorCreateReturnsASetupKeyThatSignsInLikeLogin(t *testing.T) {
	handler := operatorServer(t)
	rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken,
		`{"email":"new@example.com","limits":{"storage_bytes":50000000000,"devices":5,"searches_per_month":1000,"app_actions_per_month":1000}}`)
	if rec.Code != http.StatusCreated || body["created"] != true {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
	// searches_per_month is retired: billing may still send it, and it is ignored.
	if accountField(body, "limits", "devices") != float64(5) || accountField(body, "limits", "searches_per_month") != nil || accountField(body, "setup", "live_key") != true {
		t.Fatalf("account = %v", body["account"])
	}
	key := issuedKey(t, body)
	if accountField(body, "setup_link") != "https://mistysys.com/setup#server=cloud.misty.example&key="+key || !strings.HasPrefix(key, "misty_setup_") || len(key) != len("misty_setup_")+32 {
		t.Fatalf("key = %q, link = %v", key, accountField(body, "setup_link"))
	}
	if _, emailed := body["setup_key_sent"]; emailed {
		t.Fatalf("the server sends no email: %v", body)
	}
	// Created again (a retried webhook): same account, and no second key
	// while the first still works.
	rec, body = call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"NEW@example.com","limits":{"devices":3}}`)
	if rec.Code != http.StatusOK || body["created"] != false || accountField(body, "setup_key") != nil || accountField(body, "setup_link") != nil {
		t.Fatalf("retried create = %d %v", rec.Code, body)
	}
	// Looked up by its exact email, whatever the case.
	if rec, list := call(t, handler, http.MethodGet, "/operator/v1/accounts?email=New@Example.com", operatorToken, ""); rec.Code != http.StatusOK || len(list["accounts"].([]any)) != 1 {
		t.Fatalf("by email = %d %v", rec.Code, list)
	}
	if _, list := call(t, handler, http.MethodGet, "/operator/v1/accounts?email=new@example", operatorToken, ""); len(list["accounts"].([]any)) != 0 {
		t.Fatalf("a fragment matched an exact email: %v", list)
	}

	rec, redeemed := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+strings.ToUpper(key)+`"}`)
	if rec.Code != http.StatusOK || redeemed["email"] != "new@example.com" || redeemed["provider"] != "misty" {
		t.Fatalf("redeem = %d %v", rec.Code, redeemed)
	}
	for _, field := range []string{"user_id", "username", "email", "provider", "credential_account"} {
		if _, ok := redeemed[field]; !ok {
			t.Fatalf("redeem response lacks login's %q: %v", field, redeemed)
		}
	}
	names := map[string]bool{}
	for _, cookie := range rec.Result().Cookies() {
		names[cookie.Name] = cookie.Value != ""
	}
	if !names[SessionCookieName] || !names[RefreshCookieName] {
		t.Fatalf("redeem cookies = %v", names)
	}
	if again, body := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+key+`"}`); again.Code != http.StatusUnauthorized || body["code"] != "setup_key_invalid" {
		t.Fatalf("second redeem = %d %v", again.Code, body)
	}
	// Once signed in, a repeat create issues nothing.
	if _, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"new@example.com"}`); accountField(body, "setup_key") != nil {
		t.Fatalf("create after sign-in issued a key: %v", body)
	}
}

// A setup key for an account that already has a password signs it in and
// lets it choose a new password: the server's only password reset.
func TestSetupKeyResetsAForgottenPassword(t *testing.T) {
	handler := operatorServer(t)
	_, list := call(t, handler, http.MethodGet, "/operator/v1/accounts?email=owner@example.com", operatorToken, "")
	owner, _ := list["accounts"].([]any)[0].(map[string]any)
	id, _ := owner["id"].(string)
	rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/setup-key", operatorToken, "")
	if rec.Code != http.StatusOK || accountField(body, "owner") != true {
		t.Fatalf("setup key = %d %v", rec.Code, body)
	}
	rec, redeemed := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+issuedKey(t, body)+`"}`)
	reset, _ := redeemed["password_reset_token"].(string)
	if rec.Code != http.StatusOK || reset == "" {
		t.Fatalf("redeem = %d %v", rec.Code, redeemed)
	}
	session := rec.Result().Cookies()
	if rec, _ := call(t, handler, http.MethodPut, "/v1/me/password", "", `{"current_password":"","new_password":"a new long password"}`, session...); rec.Code != http.StatusUnauthorized {
		t.Fatalf("change without the reset token = %d", rec.Code)
	}
	if rec, _ := call(t, handler, http.MethodPut, "/v1/me/password", "", `{"reset_token":"`+reset+`","new_password":"a new long password","credential":true}`, session...); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d", rec.Code)
	}
	if rec, _ := call(t, handler, http.MethodPost, "/v1/login", "", `{"email":"owner@example.com","password":"a new long password"}`); rec.Code != http.StatusOK {
		t.Fatalf("login with the new password = %d", rec.Code)
	}
}

func TestOperatorLimitsPatchSetsOnlyNamedFields(t *testing.T) {
	handler := operatorServer(t)
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"limits@example.com","limits":{"devices":5,"app_actions_per_month":1000}}`)
	id, _ := accountField(body, "id").(string)
	rec, body := call(t, handler, http.MethodPatch, "/operator/v1/accounts/"+id, operatorToken, `{"limits":{"devices":null,"storage_bytes":150000000000}}`)
	if rec.Code != http.StatusOK || accountField(body, "limits", "devices") != nil ||
		accountField(body, "limits", "storage_bytes") != float64(150000000000) || accountField(body, "limits", "app_actions_per_month") != float64(1000) {
		t.Fatalf("patch = %d %v", rec.Code, body)
	}
	for _, bad := range []string{`{"limits":{"seats":3}}`, `{"limits":{"devices":-1}}`, `{"limits":{"devices":"5"}}`} {
		if rec, body := call(t, handler, http.MethodPatch, "/operator/v1/accounts/"+id, operatorToken, bad); rec.Code != http.StatusBadRequest || body["code"] != "invalid_request" {
			t.Fatalf("%s = %d %v", bad, rec.Code, body)
		}
	}
	if rec, body := call(t, handler, http.MethodPatch, "/operator/v1/accounts/nobody", operatorToken, `{"limits":{}}`); rec.Code != http.StatusNotFound || body["code"] != "account_not_found" {
		t.Fatalf("unknown account = %d %v", rec.Code, body)
	}
}

func TestSuspendedAccountIsRefusedEverywhereUntilResumed(t *testing.T) {
	handler := operatorServer(t)
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"paused@example.com","limits":{}}`)
	id, _ := accountField(body, "id").(string)
	rec, _ := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+issuedKey(t, body)+`"}`)
	session := rec.Result().Cookies()
	if rec, _ := call(t, handler, http.MethodGet, "/v1/me", "", "", session...); rec.Code != http.StatusOK {
		t.Fatalf("me before suspend = %d", rec.Code)
	}
	if rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/suspend", operatorToken, ""); rec.Code != http.StatusOK || accountField(body, "suspended") != true {
		t.Fatalf("suspend = %d %v", rec.Code, body)
	}
	if rec, body := call(t, handler, http.MethodGet, "/v1/me", "", "", session...); rec.Code != http.StatusForbidden || body["code"] != "account_suspended" {
		t.Fatalf("me while suspended = %d %v", rec.Code, body)
	}
	rec, body = call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/setup-key", operatorToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("new key = %d %v", rec.Code, body)
	}
	key := issuedKey(t, body)
	if rec, body := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+key+`"}`); rec.Code != http.StatusForbidden || body["code"] != "account_suspended" {
		t.Fatalf("redeem while suspended = %d %v", rec.Code, body)
	}
	if rec, _ := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/resume", operatorToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("resume = %d", rec.Code)
	}
	if rec, _ := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+key+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("redeem after resume = %d", rec.Code)
	}
}

func TestSetupKeyRedeemRejectsMalformedKeysWithoutTheDatabase(t *testing.T) {
	for _, key := range []string{"", "misty_setup_short", "misty_setup_" + strings.Repeat("1", 32), "other_" + strings.Repeat("a", 32)} {
		if _, ok := NormalizeSetupKey(key); ok {
			t.Fatalf("%q accepted", key)
		}
	}
	generated, err := GenerateSetupKey()
	if err != nil {
		t.Fatal(err)
	}
	if normalized, ok := NormalizeSetupKey("  " + strings.ToUpper(generated) + "\n"); !ok || normalized != generated {
		t.Fatalf("generated key %q normalized to %q %v", generated, normalized, ok)
	}
}
