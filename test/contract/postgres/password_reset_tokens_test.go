package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/db"

	"github.com/kannachi323/misty/server/internal/platform/security"
	"golang.org/x/crypto/bcrypt"
)

// A setup key's sign-in may set a new password without the old one, once.
func TestPasswordResetGrantLifecycle(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	user, err := database.CreateUser("reset-repo@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("reset-token")
	if err := database.GrantPasswordReset(ctx, user.ID, tokenHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("GrantPasswordReset() error = %v", err)
	}
	if err := database.CreateRefreshSession(ctx, "family", "refresh", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := database.ResetPassword(ctx, "someone-else", tokenHash, "new-password", "", true, time.Now()); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatalf("ResetPassword() for another account error = %v", err)
	}
	if err := database.ResetPassword(ctx, user.ID, tokenHash, "new-password", "", true, time.Now()); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if err := database.ResetPassword(ctx, user.ID, tokenHash, "newer-password", "", true, time.Now()); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatalf("ResetPassword() reused the grant: %v", err)
	}

	if ok, err := database.RotateRefreshSession(ctx, "family", "refresh", "next", user.ID); err != nil || ok {
		t.Fatalf("password reset did not revoke refresh session: %v %v", ok, err)
	}
	reset, hash, err := database.GetUserByEmail("reset-repo@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password")); err != nil {
		t.Fatalf("new password hash comparison failed: %v", err)
	}
	if !reset.CredentialPassword {
		t.Fatal("a reset with a derived credential is not marked as one")
	}
}

func TestExpiredPasswordResetGrantIsRefused(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	user, err := database.CreateUser("expired-reset@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("expired-reset-token")
	if err := database.GrantPasswordReset(ctx, user.ID, tokenHash, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("GrantPasswordReset() error = %v", err)
	}
	if err := database.ResetPassword(ctx, user.ID, tokenHash, "new-password", "", true, time.Now()); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatalf("ResetPassword() error = %v, want %v", err, ErrPasswordResetTokenInvalid)
	}
	_, hash, err := database.GetUserByEmail("expired-reset@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("old-password")); err != nil {
		t.Fatalf("an expired grant changed the password: %v", err)
	}
}
