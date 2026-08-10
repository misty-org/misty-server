package db

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/kannachi323/misty/server/internal/platform/security"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordResetTokenLifecycle(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Reset Repo User", "reset-repo@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("reset-token")
	if err := database.UpsertPasswordResetToken(user.ID, tokenHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("UpsertPasswordResetToken() error = %v", err)
	}
	if err := database.ValidatePasswordResetToken(tokenHash, time.Now()); err != nil {
		t.Fatalf("ValidatePasswordResetToken() error = %v", err)
	}
	if err := database.ResetPasswordWithToken(tokenHash, "new-password", time.Now()); err != nil {
		t.Fatalf("ResetPasswordWithToken() error = %v", err)
	}
	if err := database.ValidatePasswordResetToken(tokenHash, time.Now()); err != ErrPasswordResetTokenInvalid {
		t.Fatalf("ValidatePasswordResetToken() after reset error = %v, want %v", err, ErrPasswordResetTokenInvalid)
	}

	_, hash, err := database.GetUserByEmail("reset-repo@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password")); err != nil {
		t.Fatalf("new password hash comparison failed: %v", err)
	}
}

func TestValidatePasswordResetTokenDeletesExpiredToken(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Expired Reset", "expired-reset@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("expired-reset-token")
	if err := database.UpsertPasswordResetToken(user.ID, tokenHash, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("UpsertPasswordResetToken() error = %v", err)
	}
	if err := database.ValidatePasswordResetToken(tokenHash, time.Now()); err != ErrPasswordResetTokenInvalid {
		t.Fatalf("ValidatePasswordResetToken() error = %v, want %v", err, ErrPasswordResetTokenInvalid)
	}

	var count int
	if err := database.Conn.QueryRow(`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = $1`, user.ID).Scan(&count); err != nil {
		t.Fatalf("count query error = %v", err)
	}
	if count != 0 {
		t.Fatalf("expired password reset token rows = %d, want 0", count)
	}
}
