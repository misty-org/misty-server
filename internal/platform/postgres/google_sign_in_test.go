package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/lib/pq"
)

// These focused integration tests need only the account tables; they can run
// against disposable PostgreSQL without pgvector or the unrelated app schema.
func googleTestDatabase(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("MISTY_GOOGLE_AUTH_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_google_sign_in_test PostgreSQL")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = admin.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "misty_google_sign_in_test" {
		admin.Close()
		t.Fatal("refusing non-disposable database")
	}
	schema := "google_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	scoped, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		scoped.Close()
		admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		admin.Close()
	})
	baseline, err := migrationFiles.ReadFile("migrations/20270224000000_browser_workspace_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.ReplaceAll(string(baseline), "\r\n", "\n")
	for _, table := range []string{"users", "licenses", "sessions", "password_reset_tokens"} {
		_, rest, ok := strings.Cut(raw, "CREATE TABLE public."+table+" (")
		if !ok {
			t.Fatal(table)
		}
		definition, _, ok := strings.Cut(rest, "\n);")
		if !ok {
			t.Fatal(table)
		}
		if _, err = scoped.Exec("CREATE TABLE " + table + " (" + definition + "\n)"); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`ALTER TABLE users ADD PRIMARY KEY(id)`,
		`ALTER TABLE licenses ADD PRIMARY KEY(id)`,
		`ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE(email)`,
		`CREATE UNIQUE INDEX users_email_normalized_unique_idx ON users(lower(email))`,
		`CREATE UNIQUE INDEX users_username_unique_idx ON users(username)`,
		`ALTER TABLE password_reset_tokens ADD PRIMARY KEY(user_id)`,
		`INSERT INTO users(id,license_id,name,username,email,password_hash) VALUES('legacy','legacy-license','Legacy','legacy','legacy@example.com','legacy-hash')`,
		`CREATE OR REPLACE FUNCTION public.misty_rls_is_service() RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT current_setting('app.rls_mode',true)='service' $$`,
	} {
		if _, err = scoped.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := migrationFiles.ReadFile("migrations/20271001000000_google_sign_in.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scoped.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	return &Database{Conn: scoped}
}

func TestGoogleConcurrentProvisioning(t *testing.T) {
	db := googleTestDatabase(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user, err := db.GoogleUser("Concurrent", "parallel@example.com", "parallel-subject")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- user.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatal("multiple accounts")
		}
	}
	var count int
	if err := db.Conn.QueryRow(`SELECT count(*) FROM users WHERE email='parallel@example.com'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("users=%d %v", count, err)
	}
}

func TestGoogleFlowExpiryAndSingleUse(t *testing.T) {
	db := googleTestDatabase(t)
	ctx := context.Background()
	user, err := db.GoogleUser("Google", "flow@example.com", "flow-subject")
	if err != nil {
		t.Fatal(err)
	}
	state, poll := security.HashToken("state"), security.HashToken("poll")
	flow := GoogleSignInFlow{StateHash: state, PollHash: poll, Nonce: "nonce", Verifier: "verifier", ExpiresAt: time.Now().Add(time.Minute)}
	if err = db.CreateGoogleSignInFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConsumeGoogleSignInFlow(ctx, state); !errors.Is(err, ErrGoogleFlowInvalid) {
		t.Fatal("state redeemed session")
	}
	if value, err := db.ConsumeGoogleSignInFlow(ctx, poll); err != nil || value != nil {
		t.Fatalf("pending: %v %v", value, err)
	}
	if _, err = db.AdvanceGoogleSignInFlow(ctx, state, "pending", "started"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AdvanceGoogleSignInFlow(ctx, state, "pending", "started"); !errors.Is(err, ErrGoogleFlowInvalid) {
		t.Fatal("launch replay")
	}
	if _, err = db.AdvanceGoogleSignInFlow(ctx, state, "started", "exchanging"); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishGoogleSignInFlow(ctx, state, user.ID, ""); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := db.ConsumeGoogleSignInFlow(ctx, poll)
			if err == nil && value != nil {
				winners.Add(1)
				if value.UserID != user.ID {
					t.Error("wrong user")
				}
			} else if !errors.Is(err, ErrGoogleFlowInvalid) {
				t.Errorf("unexpected result %v %v", value, err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("redemptions=%d", winners.Load())
	}
	flow.ExpiresAt = time.Now().Add(-time.Second)
	if err = db.CreateGoogleSignInFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AdvanceGoogleSignInFlow(ctx, state, "pending", "started"); !errors.Is(err, ErrGoogleFlowInvalid) {
		t.Fatal("expired flow launched")
	}
	if _, err = db.ConsumeGoogleSignInFlow(ctx, poll); !errors.Is(err, ErrGoogleFlowInvalid) {
		t.Fatal("expired flow redeemed")
	}
}

func TestGoogleReauthenticationProofs(t *testing.T) {
	database := googleTestDatabase(t)
	ctx := context.Background()
	user, err := database.GoogleUser("Google", "reauth@example.com", "reauth-subject")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.GoogleUser("Other", "other@example.com", "other-subject")
	if err != nil {
		t.Fatal(err)
	}
	if err = database.CreateGoogleReauthenticationToken(ctx, user.ID, security.HashToken("proof")); err != nil {
		t.Fatal(err)
	}
	if valid, err := database.VerifyAccountReauthentication(ctx, other.ID, "", "proof"); err != nil || valid {
		t.Fatal("proof used for another account")
	}
	if valid, err := database.VerifyAccountReauthentication(ctx, user.ID, "password", ""); err != nil || valid {
		t.Fatal("Google password accepted")
	}
	if valid, err := database.VerifyAccountReauthentication(ctx, user.ID, "", "proof"); err != nil || !valid {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if valid, err := database.VerifyAccountReauthentication(ctx, user.ID, "", "proof"); err != nil || valid {
		t.Fatal("proof replay accepted")
	}
	if err = database.CreateGoogleReauthenticationToken(ctx, user.ID, security.HashToken("expired")); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Conn.Exec(`UPDATE google_reauthentication_tokens SET expires_at=NOW()-INTERVAL '1 second'`); err != nil {
		t.Fatal(err)
	}
	if valid, err := database.VerifyAccountReauthentication(ctx, user.ID, "", "expired"); err != nil || valid {
		t.Fatal("expired proof accepted")
	}
}
