package api

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/test/testkit"
)

const readOnlyPassword = "a-long-enough-password"

// readOnlyServer mounts sign-in, reads, the account export and a few writes
// behind the guard, as the app mounts them.
func readOnlyServer(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv("MISTY_PUBLIC_API_URL", "https://cloud.misty.example/v1")
	database := testkit.OpenDatabase(t)
	spaces, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	guard := NewSuspensionGuard(database)
	router := chi.NewRouter()
	router.Use(guard.Middleware)
	router.Post("/v1/login", Login(database))
	router.Post("/v1/auth/refresh", RefreshSession(database))
	router.Get("/v1/me", GetMe(database))
	router.Put("/v1/me/password", ChangePassword(database))
	router.Post("/v1/me/export", spaces.AccountExportManifest())
	router.MethodFunc(http.MethodGet, "/v1/spaces", spaces.Spaces())
	router.MethodFunc(http.MethodPost, "/v1/spaces", spaces.Spaces())
	router.Route("/operator/v1", func(r chi.Router) {
		r.Use(RequireOperator(operatorToken))
		NewOperatorAccounts(database, guard).Mount(r)
		r.Post("/accounts/{id}/export", spaces.OperatorAccountExport())
	})
	if _, err := database.CreateUser("reader@example.com", readOnlyPassword); err != nil {
		t.Fatal(err)
	}
	return router
}

func signIn(t *testing.T, handler http.Handler) []*http.Cookie {
	t.Helper()
	rec, body := call(t, handler, http.MethodPost, "/v1/login", "", `{"email":"reader@example.com","password":"`+readOnlyPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %v", rec.Code, body)
	}
	return rec.Result().Cookies()
}

func TestClosingAccountReadsAndExportsButCannotChangeAnything(t *testing.T) {
	handler := readOnlyServer(t)
	session := signIn(t, handler)
	_, me := call(t, handler, http.MethodGet, "/v1/me", "", "", session...)
	id, _ := me["id"].(string)
	if me["delete_at"] != nil {
		t.Fatalf("delete_at before closing = %v", me["delete_at"])
	}

	rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, `{"delete_after_days":30}`)
	deleteAt, _ := accountField(body, "delete_at").(string)
	if rec.Code != http.StatusOK || accountField(body, "suspended") != false || deleteAt == "" {
		t.Fatalf("close = %d %v", rec.Code, body)
	}
	at, _ := time.Parse(time.RFC3339Nano, deleteAt)

	// Reads, refresh and the export still work.
	if rec, body := call(t, handler, http.MethodGet, "/v1/me", "", "", session...); rec.Code != http.StatusOK || body["delete_at"] != at.UTC().Format(time.RFC3339) {
		t.Fatalf("me while closing = %d %v", rec.Code, body)
	}
	if rec, _ := call(t, handler, http.MethodGet, "/v1/spaces", "", "", session...); rec.Code != http.StatusOK {
		t.Fatalf("spaces while closing = %d", rec.Code)
	}
	if rec, body := call(t, handler, http.MethodPost, "/v1/me/export", "", `{"password":"`+readOnlyPassword+`"}`, session...); rec.Code != http.StatusOK || body["account_data"] == nil {
		t.Fatalf("export while closing = %d %v", rec.Code, body)
	}
	if rec, body := call(t, handler, http.MethodPost, "/v1/auth/refresh", "", "", session...); rec.Code/100 != 2 {
		t.Fatalf("refresh while closing = %d %v", rec.Code, body)
	}
	session = signIn(t, handler)

	// Changes answer account_closing with the date and how to keep the account.
	for _, write := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/spaces", `{"name":"New"}`},
		{http.MethodPost, "/v1/spaces/space_1/library/uploads", `{}`},
	} {
		rec, body := call(t, handler, write.method, write.path, "", write.body, session...)
		message, _ := body["message"].(string)
		if rec.Code != http.StatusForbidden || body["code"] != "account_closing" || body["delete_at"] != at.UTC().Format(time.RFC3339) ||
			!strings.Contains(message, at.UTC().Format("January 2, 2006")) || !strings.Contains(message, "Subscribe again") {
			t.Fatalf("%s %s while closing = %d %v", write.method, write.path, rec.Code, body)
		}
	}
	// Its password still changes, so a sign-in link can reset a forgotten one
	// and the customer can export.
	if rec, body := call(t, handler, http.MethodPut, "/v1/me/password", "", `{"current_password":"`+readOnlyPassword+`","new_password":"another-long-password"}`, session...); rec.Code != http.StatusOK {
		t.Fatalf("password change while closing = %d %v", rec.Code, body)
	}

	// Resuming makes it writable again.
	if rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/resume", operatorToken, ""); rec.Code != http.StatusOK || accountField(body, "delete_at") != nil {
		t.Fatalf("resume = %d %v", rec.Code, body)
	}
	if rec, body := call(t, handler, http.MethodPost, "/v1/spaces", "", `{"name":"New"}`, session...); rec.Code == http.StatusForbidden {
		t.Fatalf("create after resume = %d %v", rec.Code, body)
	}
}

func TestSuspensionStillWinsOverClosing(t *testing.T) {
	handler := readOnlyServer(t)
	session := signIn(t, handler)
	_, me := call(t, handler, http.MethodGet, "/v1/me", "", "", session...)
	id, _ := me["id"].(string)
	for _, step := range []string{"/close", "/suspend"} {
		if rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+step, operatorToken, "{}"); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %v", step, rec.Code, body)
		}
	}
	if rec, body := call(t, handler, http.MethodGet, "/v1/me", "", "", session...); rec.Code != http.StatusForbidden || body["code"] != "account_suspended" {
		t.Fatalf("me while suspended and closing = %d %v", rec.Code, body)
	}
	if rec, body := call(t, handler, http.MethodPost, "/v1/login", "", `{"email":"reader@example.com","password":"`+readOnlyPassword+`"}`); rec.Code != http.StatusForbidden || body["code"] != "account_suspended" {
		t.Fatalf("login while suspended and closing = %d %v", rec.Code, body)
	}
	if rec, body := call(t, handler, http.MethodPost, "/v1/me/export", "", `{"password":"`+readOnlyPassword+`"}`, session...); rec.Code != http.StatusForbidden || body["code"] != "account_suspended" {
		t.Fatalf("export while suspended = %d %v", rec.Code, body)
	}
}
