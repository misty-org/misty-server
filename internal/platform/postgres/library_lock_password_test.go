package db

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/lib/pq"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func libraryPasswordTestDatabase(t *testing.T) *Database {
	t.Helper()
	database := googleTestDatabase(t)
	if _, err := database.Conn.Exec(`CREATE OR REPLACE FUNCTION public.misty_rls_user_id() RETURNS text LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.current_user_id',true),'') $$;
 CREATE TABLE library_reauthentication_grants(token_hash text);
 INSERT INTO library_reauthentication_grants VALUES('old-account-password-grant');`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/20271001010000_library_lock_password.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Conn.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = database.Conn.QueryRow(`SELECT count(*) FROM library_reauthentication_grants`).Scan(&count); err != nil || count != 0 {
		t.Fatal("old account-password grants survived migration")
	}
	return database
}

func TestLibraryPasswordIsSeparateForEveryProvider(t *testing.T) {
	database := libraryPasswordTestDatabase(t)
	ctx := context.Background()
	passwordUser, err := database.CreateUser("Password User", "password-user@example.com", "account-password")
	if err != nil {
		t.Fatal(err)
	}
	googleUser, err := database.GoogleUser("Google User", "google-user@example.com", "google-lock-subject")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*User{passwordUser, googleUser} {
		t.Run(user.Provider, func(t *testing.T) {
			if configured, err := database.LibraryPasswordConfigured(ctx, user.ID); err != nil || configured {
				t.Fatal("new account already has a library password")
			}
			if valid, err := database.VerifyLibraryPassword(ctx, user.ID, "account-password"); valid || !errors.Is(err, ErrLibraryPasswordNotSet) {
				t.Fatal("account password used before library setup")
			}
			if err := database.SetInitialLibraryPassword(ctx, user.ID, "short"); !errors.Is(err, ErrLibraryPasswordInvalid) {
				t.Fatal("short password allowed")
			}
			if err := database.SetInitialLibraryPassword(ctx, user.ID, strings.Repeat("x", 73)); !errors.Is(err, ErrLibraryPasswordInvalid) {
				t.Fatal("oversize bcrypt input allowed")
			}
			if err := database.SetInitialLibraryPassword(ctx, user.ID, "library-password-"+user.Provider); err != nil {
				t.Fatal(err)
			}
			if configured, err := database.LibraryPasswordConfigured(ctx, user.ID); err != nil || !configured {
				t.Fatal("library password not persisted")
			}
			if valid, err := database.VerifyLibraryPassword(ctx, user.ID, "library-password-"+user.Provider); err != nil || !valid {
				t.Fatalf("library password rejected: %v", err)
			}
			if valid, err := database.VerifyLibraryPassword(ctx, user.ID, "account-password"); err != nil || valid {
				t.Fatal("account password unlocks library")
			}
			if valid, err := database.VerifyUserPassword(ctx, user.ID, "library-password-"+user.Provider); err != nil || valid {
				t.Fatal("library password signs into account")
			}
			if err := database.SetInitialLibraryPassword(ctx, user.ID, "replacement-password"); !errors.Is(err, ErrLibraryPasswordAlreadySet) {
				t.Fatal("initial setup overwrote credential")
			}
			var hash string
			if err := database.Conn.QueryRow(`SELECT password_hash FROM library_lock_credentials WHERE user_id=$1`, user.ID).Scan(&hash); err != nil || strings.Contains(hash, "library-password") {
				t.Fatal("plaintext library password persisted")
			}
		})
	}
	if valid, err := database.VerifyLibraryPassword(ctx, googleUser.ID, "library-password-misty"); err != nil || valid {
		t.Fatal("another account's library password accepted")
	}
	reset := security.HashToken("reset")
	if err := database.UpsertPasswordResetToken(passwordUser.ID, reset, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := database.ResetPasswordWithToken(reset, "changed-account-password", time.Now()); err != nil {
		t.Fatal(err)
	}
	if valid, err := database.VerifyLibraryPassword(ctx, passwordUser.ID, "library-password-misty"); err != nil || !valid {
		t.Fatal("account password reset altered library lock")
	}
}

func TestLibraryPasswordConcurrentSetupCannotOverwrite(t *testing.T) {
	database := libraryPasswordTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Race", "race-lock@example.com", "account-password")
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for _, password := range []string{"first-library-password", "second-library-password"} {
		wg.Add(1)
		go func(password string) {
			defer wg.Done()
			err := database.SetInitialLibraryPassword(ctx, user.ID, password)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrLibraryPasswordAlreadySet) {
				t.Error(err)
			}
		}(password)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("setup winners=%d", winners.Load())
	}
	first, _ := database.VerifyLibraryPassword(ctx, user.ID, "first-library-password")
	second, _ := database.VerifyLibraryPassword(ctx, user.ID, "second-library-password")
	if first == second {
		t.Fatal("exactly one password must remain valid")
	}
}

func TestLibraryCredentialsAndGoogleFlowsRespectRLS(t *testing.T) {
	database := libraryPasswordTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Owner", "rls-owner@example.com", "account-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other", "rls-other@example.com", "account-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*User{owner, other} {
		if err = database.SetInitialLibraryPassword(ctx, user.ID, "library-password"); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.CreateGoogleSignInFlow(ctx, GoogleSignInFlow{StateHash: "state-hash", PollHash: "poll-hash", Nonce: "nonce", Verifier: "verifier", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err = database.Conn.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	role := pq.QuoteIdentifier("auth_rls_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err = database.Conn.Exec(`CREATE ROLE ` + role + ` NOLOGIN; GRANT USAGE ON SCHEMA ` + pq.QuoteIdentifier(schema) + ` TO ` + role + `; GRANT SELECT ON ALL TABLES IN SCHEMA ` + pq.QuoteIdentifier(schema) + ` TO ` + role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := database.Conn.Exec(`DROP OWNED BY ` + role + `; DROP ROLE ` + role); err != nil {
			t.Error(err)
		}
	})
	tx, err := database.Conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SET LOCAL ROLE ` + role); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM library_lock_credentials`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unscoped role can read library hashes")
	}
	if _, err = tx.Exec(`SELECT set_config('app.current_user_id',$1,true)`, owner.ID); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = tx.QueryRow(`SELECT user_id FROM library_lock_credentials`).Scan(&id); err != nil || id != owner.ID {
		t.Fatal("owner cannot read own lock")
	}
	if err = tx.QueryRow(`SELECT count(*) FROM library_lock_credentials WHERE user_id=$1`, other.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("owner can read another account lock")
	}
	if err = tx.QueryRow(`SELECT count(*) FROM google_sign_in_flows`).Scan(&count); err != nil || count != 0 {
		t.Fatal("user can read Google flow secrets")
	}
	if _, err = tx.Exec(`SELECT set_config('app.rls_mode','service',true)`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`SELECT count(*) FROM google_sign_in_flows`).Scan(&count); err != nil || count != 1 {
		t.Fatal("service cannot read Google flows")
	}
}
