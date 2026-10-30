package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Runs against a disposable database with every migration applied.
func accountVerificationDB(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("MISTY_ACCOUNT_VERIFICATION_TEST_DSN")
	if dsn == "" {
		t.Skip("requires a disposable misty_account_verification_test PostgreSQL with migrations applied")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var name string
	if err = conn.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || name != "misty_account_verification_test" {
		t.Fatal("refusing a non-disposable database", name, err)
	}
	return &Database{Conn: conn}
}

func TestEmailVerification(t *testing.T) {
	database := accountVerificationDB(t)
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	friend, err := database.CreateUser("friend-"+suffix+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := database.EmailVerifiedAt(ctx, friend.ID); err != nil || verified != nil {
		t.Fatal("a new password account started verified", verified, err)
	}
	token := "verification-token-" + suffix
	if err = database.UpsertEmailVerificationToken(ctx, friend.ID, security.HashToken(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if user, err := database.VerifyEmailWithToken(ctx, security.HashToken(token), time.Now()); err != nil || user != friend.ID {
		t.Fatal(user, err)
	}
	if _, err = database.VerifyEmailWithToken(ctx, security.HashToken(token), time.Now()); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatal("a verification link worked twice", err)
	}
	if verified, _ := database.EmailVerifiedAt(ctx, friend.ID); verified == nil {
		t.Fatal("verification was not recorded")
	}
}
