package integration

import (
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateUserNormalizesEmailAndCreatesLicense(t *testing.T) {
	database := openIntegrationDatabase(t)

	user, err := database.CreateUser("Ada", "  Ada@Example.com ", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.Email != "ada@example.com" {
		t.Fatalf("user.Email = %q, want %q", user.Email, "ada@example.com")
	}

	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil || license == nil {
		t.Fatalf("GetLicenseByUserID() error = %v, license = %#v", err, license)
	}
	if license.ID != user.LicenseID {
		t.Fatalf("license.ID = %q, want %q", license.ID, user.LicenseID)
	}

	if _, err := database.CreateUser("Ada Duplicate", "ada@example.com", "password123"); err == nil {
		t.Fatal("CreateUser() succeeded for duplicate email")
	}
}

func TestSessionCRUDAndExpiry(t *testing.T) {
	database := openIntegrationDatabase(t)

	user, err := database.CreateUser("Session User", "session@example.com", "password123")
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

func TestLicenseExpiredTrialAutoDowngrades(t *testing.T) {
	database := openIntegrationDatabase(t)

	user, err := database.CreateUser("Trial Repo User", "trial-repo@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	expiredAt := time.Now().Add(-time.Hour)
	if _, err := database.Conn.Exec(`
		UPDATE licenses
		SET tier = $2, status = $3, expires_at = $4, trial_started_at = $5
		WHERE user_id = $1
	`, user.ID, db.TierPro, db.LicenseStatusTrialing, expiredAt, expiredAt.Add(-24*time.Hour)); err != nil {
		t.Fatalf("license update error = %v", err)
	}

	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil || license == nil {
		t.Fatalf("GetLicenseByUserID() error = %v, license = %#v", err, license)
	}
	if license.Tier != db.TierBasic || license.Status != db.LicenseStatusActive || license.ExpiresAt != nil {
		t.Fatalf("license after auto-downgrade = %#v", license)
	}
}

func TestPasswordResetTokenPersistenceAndReset(t *testing.T) {
	database := openIntegrationDatabase(t)

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
	if err := database.ValidatePasswordResetToken(tokenHash, time.Now()); err != db.ErrPasswordResetTokenInvalid {
		t.Fatalf("ValidatePasswordResetToken() after reset error = %v, want %v", err, db.ErrPasswordResetTokenInvalid)
	}

	_, hash, err := database.GetUserByEmail("reset-repo@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password")); err != nil {
		t.Fatalf("new password hash comparison failed: %v", err)
	}
}

func TestWaitlistSignupDuplicateReturnsFalse(t *testing.T) {
	database := openIntegrationDatabase(t)

	created, err := database.CreateWaitlistSignup("Ada", "ada@example.com")
	if err != nil {
		t.Fatalf("CreateWaitlistSignup() error = %v", err)
	}
	if !created {
		t.Fatal("first waitlist signup should be created")
	}

	created, err = database.CreateWaitlistSignup("Ada Again", "ada@example.com")
	if err != nil {
		t.Fatalf("CreateWaitlistSignup() duplicate error = %v", err)
	}
	if created {
		t.Fatal("duplicate waitlist signup should return created=false")
	}
}
