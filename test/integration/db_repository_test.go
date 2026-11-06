package integration

import (
	"context"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"golang.org/x/crypto/bcrypt"
)

func TestSessionCRUDAndExpiry(t *testing.T) {
	database := openIntegrationDatabase(t)

	user, err := database.CreateUser("session@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("session-token")
	if err := database.CreateSession(tokenHash, user.ID); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	userID, err := database.GetSessionUserID(tokenHash)
	if err != nil {
		t.Fatalf("GetSessionUserID() error = %v", err)
	}
	if userID != user.ID {
		t.Fatalf("GetSessionUserID() = %q, want %q", userID, user.ID)
	}

	if _, err := database.Conn.Exec(`UPDATE sessions SET expires_at = NOW() - INTERVAL '1 minute' WHERE token_hash = $1`, tokenHash); err != nil {
		t.Fatalf("expire session update error = %v", err)
	}

	userID, err = database.GetSessionUserID(tokenHash)
	if err != nil {
		t.Fatalf("GetSessionUserID() after expiry error = %v", err)
	}
	if userID != "" {
		t.Fatalf("GetSessionUserID() after expiry = %q, want empty", userID)
	}

	if err := database.DeleteSession(tokenHash); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
}

func TestPasswordResetGrantPersistenceAndReset(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()

	user, err := database.CreateUser("reset-repo@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("reset-token")
	if err := database.GrantPasswordReset(ctx, user.ID, tokenHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("GrantPasswordReset() error = %v", err)
	}
	if err := database.ResetPassword(ctx, user.ID, tokenHash, "new-password", "", time.Now()); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if err := database.ResetPassword(ctx, user.ID, tokenHash, "new-password", "", time.Now()); err != db.ErrPasswordResetTokenInvalid {
		t.Fatalf("ResetPassword() after reset error = %v, want %v", err, db.ErrPasswordResetTokenInvalid)
	}

	_, hash, err := database.GetUserByEmail("reset-repo@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password")); err != nil {
		t.Fatalf("new password hash comparison failed: %v", err)
	}
}
