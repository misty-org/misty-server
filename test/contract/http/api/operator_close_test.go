package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kannachi323/misty/server/internal/platform/db"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"github.com/kannachi323/misty/server/test/testkit"
)

func closingServer(t *testing.T) (http.Handler, *db.Database) {
	t.Helper()
	t.Setenv("MISTY_PUBLIC_API_URL", "https://cloud.misty.example/v1")
	database := testkit.OpenDatabase(t)
	guard := NewSuspensionGuard(database)
	router := chi.NewRouter()
	router.Use(guard.Middleware)
	router.Post("/v1/setup-keys/redeem", RedeemSetupKey(database))
	router.Get("/v1/me", GetMe(database))
	router.Put("/v1/me/password", ChangePassword(database))
	router.Route("/operator/v1", func(r chi.Router) {
		r.Use(RequireOperator(operatorToken))
		NewOperatorAccounts(database, guard).Mount(r)
	})
	return router, database
}

func TestCloseMakesReadOnlyAndDeletesOnTheDateWithoutEmails(t *testing.T) {
	handler, database := closingServer(t)
	ctx := context.Background()
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"closing@example.com"}`)
	id, _ := accountField(body, "id").(string)
	rec, _ := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+issuedKey(t, body)+`"}`)
	session := rec.Result().Cookies()

	for _, bad := range []string{`{"delete_after_days":0}`, `{"delete_after_days":400}`, `{"delete_after_days":"30"}`} {
		if rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %v", bad, rec.Code, body)
		}
	}
	rec, body = call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, `{"delete_after_days":30}`)
	deleteAt, _ := time.Parse(time.RFC3339Nano, accountField(body, "delete_at").(string))
	if rec.Code != http.StatusOK || accountField(body, "suspended") != false || time.Until(deleteAt) < 29*24*time.Hour || time.Until(deleteAt) > 30*24*time.Hour {
		t.Fatalf("close = %d %v", rec.Code, body)
	}
	// Closed is read-only, not suspended: the session still reads.
	if rec, body := call(t, handler, http.MethodGet, "/v1/me", "", "", session...); rec.Code != http.StatusOK || body["delete_at"] != deleteAt.UTC().Format(time.RFC3339) {
		t.Fatalf("me while closed = %d %v", rec.Code, body)
	}
	// A retried close keeps the date it set first.
	if _, again := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, `{"delete_after_days":5}`); accountField(again, "delete_at") != accountField(body, "delete_at") {
		t.Fatalf("retried close moved the date: %v", again)
	}

	// A sign-in link still works while closing, and lets the person choose a
	// password, so a customer who forgot theirs can sign in and export.
	_, body = call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/setup-key", operatorToken, "")
	rec, redeemed := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+issuedKey(t, body)+`"}`)
	reset, _ := redeemed["password_reset_token"].(string)
	if rec.Code != http.StatusOK || reset == "" {
		t.Fatalf("redeem while closing = %d %v", rec.Code, redeemed)
	}
	if rec, body := call(t, handler, http.MethodPut, "/v1/me/password", "", `{"reset_token":"`+reset+`","new_password":"a long enough password","credential":true}`, rec.Result().Cookies()...); rec.Code != http.StatusOK {
		t.Fatalf("password while closing = %d %v", rec.Code, body)
	}

	// Nothing is due before the date, and nothing is sent.
	closings := NewAccountClosings(database)
	if started, err := closings.Process(ctx, 10); err != nil || started != 0 {
		t.Fatalf("before the date = %d %v", started, err)
	}
	if due, err := database.DueClosedAccounts(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("due before the date = %v %v", due, err)
	}
	if _, err := database.Conn.Exec(`UPDATE account_controls SET delete_at=now()-interval '1 minute' WHERE user_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if due, err := database.DueClosedAccounts(ctx, 10); err != nil || len(due) != 1 || due[0] != id {
		t.Fatalf("due on the date = %v %v", due, err)
	}
	if _, err := database.Conn.Exec(`UPDATE account_controls SET delete_at=now()+interval '2 days' WHERE user_id=$1`, id); err != nil {
		t.Fatal(err)
	}

	rec, body = call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/resume", operatorToken, "")
	if rec.Code != http.StatusOK || accountField(body, "suspended") != false || accountField(body, "delete_at") != nil {
		t.Fatalf("resume = %d %v", rec.Code, body)
	}
	if due, err := database.DueClosedAccounts(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("due after resume = %v %v", due, err)
	}
}

func TestPatchSetsAndClearsThePeriodAnchor(t *testing.T) {
	handler, _ := closingServer(t)
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"anchor@example.com","limits":{"app_actions_per_month":1000}}`)
	id, _ := accountField(body, "id").(string)
	rec, body := call(t, handler, http.MethodPatch, "/operator/v1/accounts/"+id, operatorToken, `{"period_anchor":"2026-01-31T08:00:00Z"}`)
	if rec.Code != http.StatusOK || accountField(body, "period_anchor") != "2026-01-31T08:00:00Z" || accountField(body, "limits", "app_actions_per_month") != float64(1000) {
		t.Fatalf("anchor = %d %v", rec.Code, body)
	}
	start, _ := time.Parse(time.RFC3339, accountField(body, "usage", "period_start").(string))
	if day := start.Day(); day != 31 && day != time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day() {
		t.Fatalf("period start %v for an anchor on the 31st", start)
	}
	if rec, body := call(t, handler, http.MethodPatch, "/operator/v1/accounts/"+id, operatorToken, `{"period_anchor":"tomorrow"}`); rec.Code != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("bad anchor = %d %v", rec.Code, body)
	}
	rec, body = call(t, handler, http.MethodPatch, "/operator/v1/accounts/"+id, operatorToken, `{"period_anchor":null}`)
	start, _ = time.Parse(time.RFC3339, accountField(body, "usage", "period_start").(string))
	if rec.Code != http.StatusOK || accountField(body, "period_anchor") != nil || start.Day() != 1 {
		t.Fatalf("cleared anchor = %d %v", rec.Code, body)
	}
}

func TestCreateForAnEmailBeingDeletedAnswers409(t *testing.T) {
	handler, database := closingServer(t)
	user, err := database.CreateUser("leaving@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BeginAccountDeletion(context.Background(), user.ID, "deletion_leaving", "leaving-token-hash", 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"leaving@example.com"}`); rec.Code != http.StatusConflict || body["code"] != "account_pending_deletion" {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
}
