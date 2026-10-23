package accounts

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// Runs ChangePassword's SQL against a throwaway PostgreSQL. Set
// MISTY_KEY_TRANSITION_TEST_DSN (key=value form) to an empty database whose name
// contains "test" or "check"; the test creates one small table in it holding
// only the columns this code reads. Row-level security is not part of this
// check: the scope the store passes is applied by the shared adapter.
func TestChangePasswordAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("MISTY_KEY_TRANSITION_TEST_DSN")
	if dsn == "" {
		t.Skip("set MISTY_KEY_TRANSITION_TEST_DSN to an empty throwaway database to run this")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var name string
	if err = conn.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "test") && !strings.Contains(name, "check") {
		t.Fatalf("refusing to create a table in %q: not a throwaway database", name)
	}
	for _, statement := range []string{
		`DROP TABLE IF EXISTS public.users, public.sessions`,
		`CREATE TABLE public.sessions (token_hash text PRIMARY KEY, user_id text NOT NULL)`,
		`CREATE TABLE public.users (id text PRIMARY KEY, password_hash text NOT NULL, provider text NOT NULL, lifecycle_state text NOT NULL DEFAULT 'active', credential_password boolean NOT NULL DEFAULT false)`,
	} {
		if _, err = conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.Exec(`DROP TABLE IF EXISTS public.users, public.sessions`)
	store := Store{Transaction: func(ctx context.Context, _ map[string]string, work func(*sql.Tx) error) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = work(tx); err != nil {
			return err
		}
		return tx.Commit()
	}}
	hash := func(password string) string {
		out, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	stored := func(id string) string {
		var value string
		if err := conn.QueryRow(`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for id, row := range map[string][2]string{"password-account": {"misty", "typed password"}, "google-account": {"google", "unused"}} {
		if _, err = conn.Exec(`INSERT INTO users(id,password_hash,provider) VALUES($1,$2,$3)`, id, hash(row[1]), row[0]); err != nil {
			t.Fatal(err)
		}
	}

	for _, row := range [][2]string{{"this-session", "password-account"}, {"stolen-session", "password-account"}, {"someone-elses", "google-account"}} {
		if _, err = conn.Exec(`INSERT INTO sessions(token_hash,user_id) VALUES($1,$2)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	sessions := func() []string {
		rows, err := conn.Query(`SELECT token_hash FROM sessions ORDER BY token_hash`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				t.Fatal(err)
			}
			out = append(out, hash)
		}
		return out
	}

	// A wrong current password changes nothing.
	if err = store.ChangePassword("password-account", "not it", "credential-new", "this-session", true); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Fatalf("a wrong current password was accepted: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored("password-account")), []byte("typed password")) != nil {
		t.Fatal("a refused change altered the password")
	}
	if len(sessions()) != 3 {
		t.Fatal("a refused change ended a session")
	}
	// An account that holds its password as typed moves to a derived credential.
	if err = store.ChangePassword("password-account", "typed password", "credential-new", "this-session", true); err != nil {
		t.Fatalf("the change was refused: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored("password-account")), []byte("credential-new")) != nil {
		t.Fatal("the new value does not open the account")
	}
	if bcrypt.CompareHashAndPassword([]byte(stored("password-account")), []byte("typed password")) == nil {
		t.Fatal("the old password still opens the account")
	}
	// The server now records that this account's password is a derived credential.
	var flagged bool
	if err = conn.QueryRow(`SELECT credential_password FROM users WHERE id='password-account'`).Scan(&flagged); err != nil || !flagged {
		t.Fatalf("the account was not recorded as holding a credential: %v %v", flagged, err)
	}
	// Setting the plain-password state back (a reset by email does this) clears it.
	if err = store.SetCredentialPassword("password-account", false); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(`SELECT credential_password FROM users WHERE id='password-account'`).Scan(&flagged); err != nil || flagged {
		t.Fatalf("the credential flag was not cleared: %v %v", flagged, err)
	}
	if err = store.SetCredentialPassword("password-account", true); err != nil {
		t.Fatal(err)
	}
	// Every other session of the account ended; this one and another account's stayed.
	if got := strings.Join(sessions(), ","); got != "someone-elses,this-session" {
		t.Fatalf("sessions after the change: %s", got)
	}
	// The next change starts from the credential.
	if err = store.ChangePassword("password-account", "credential-new", "credential-newer", "this-session", true); err != nil {
		t.Fatalf("changing from the credential failed: %v", err)
	}
	// A Google account has no Misty password, and an unknown one is not found.
	if err = store.ChangePassword("google-account", "unused", "credential-new", "x", true); !errors.Is(err, ErrNoPasswordAccount) {
		t.Fatalf("a Google account got a password: %v", err)
	}
	if err = store.ChangePassword("nobody", "x", "y", "x", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("an unknown account was not reported: %v", err)
	}
}
