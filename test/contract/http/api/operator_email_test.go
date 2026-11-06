package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func mailjetConfigured(t *testing.T) {
	t.Helper()
	t.Setenv("MAILJET_API_KEY", "key")
	t.Setenv("MAILJET_SECRET_KEY", "secret")
	t.Setenv("MAILJET_FROM_EMAIL", "hello@example.com")
}

func outboxKinds(t *testing.T, database *db.Database, userID string) map[string]string {
	t.Helper()
	rows, err := database.Conn.QueryContext(context.Background(), `SELECT kind,state FROM email_outbox WHERE user_id=$1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var kind, state string
		if err := rows.Scan(&kind, &state); err != nil {
			t.Fatal(err)
		}
		out[kind] = state
	}
	return out
}

func TestWithoutMailjetTheServerAnswersWithLinksAndStoresNothing(t *testing.T) {
	handler, database := closingServer(t)
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"no-mail-`+uuid.NewString()+`@example.com"}`)
	id, _ := accountField(body, "id").(string)
	if issuedKey(t, body) == "" || accountField(body, "emailed") != nil {
		t.Fatalf("create = %v", body)
	}
	rec, _ := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, `{"delete_after_days":30}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("close = %d", rec.Code)
	}
	if rows := outboxKinds(t, database, id); len(rows) != 0 {
		t.Fatalf("a server without Mailjet queued %v", rows)
	}
}

func TestWithMailjetTheServerEmailsEveryLinkAndNotice(t *testing.T) {
	mailjetConfigured(t)
	handler, database := closingServer(t)
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"mail-`+uuid.NewString()+`@example.com"}`)
	id, _ := accountField(body, "id").(string)
	// The caller still gets the link, and is told the server is emailing it.
	if issuedKey(t, body) == "" || accountField(body, "emailed") != true {
		t.Fatalf("create = %v", body)
	}
	if rows := outboxKinds(t, database, id); rows[db.EmailSetup] != "pending" {
		t.Fatalf("after create: %v", rows)
	}
	// Not signed in yet, so a new key is still the welcome; the old email is replaced.
	_, again := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/setup-key", operatorToken, "")
	if accountField(again, "emailed") != true {
		t.Fatalf("setup-key = %v", again)
	}
	// Signed in: a new key is a sign-in link.
	rec, _ := call(t, handler, http.MethodPost, "/v1/setup-keys/redeem", "", `{"setup_key":"`+issuedKey(t, again)+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem = %d", rec.Code)
	}
	call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/setup-key", operatorToken, "")
	if rows := outboxKinds(t, database, id); rows[db.EmailSignIn] != "pending" {
		t.Fatalf("after signing in: %v", rows)
	}
	// Closing queues the pair, with where to keep the account; resuming drops what is unsent.
	rec, _ = call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, `{"delete_after_days":30,"keep_url":"https://example.com/keep"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("close = %d", rec.Code)
	}
	if rows := outboxKinds(t, database, id); rows[db.EmailClosed] != "pending" || rows[db.EmailDeletionWarning] != "pending" {
		t.Fatalf("after close: %v", rows)
	}
	rec, _ = call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/resume", operatorToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resume = %d", rec.Code)
	}
	if rows := outboxKinds(t, database, id); rows[db.EmailClosed] != "cancelled" || rows[db.EmailDeletionWarning] != "cancelled" {
		t.Fatalf("after resume: %v", rows)
	}
}

func TestCloseRefusesAKeepURLThatIsNotHTTPS(t *testing.T) {
	handler, _ := closingServer(t)
	_, body := call(t, handler, http.MethodPost, "/operator/v1/accounts", operatorToken, `{"email":"keep-`+uuid.NewString()+`@example.com"}`)
	id, _ := accountField(body, "id").(string)
	for _, bad := range []string{"http://example.com", "javascript:alert(1)", "example.com"} {
		rec, _ := call(t, handler, http.MethodPost, "/operator/v1/accounts/"+id+"/close", operatorToken, `{"keep_url":"`+bad+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", bad, rec.Code)
		}
	}
}
