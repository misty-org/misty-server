package db

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestStartTrialByUserIDAndExpiredTrialAutoDowngrade(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Trial User", "trial@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	started, err := database.StartTrialByUserID(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("StartTrialByUserID() error = %v", err)
	}
	if !started {
		t.Fatal("StartTrialByUserID() = false, want true")
	}

	started, err = database.StartTrialByUserID(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("StartTrialByUserID() second call error = %v", err)
	}
	if started {
		t.Fatal("StartTrialByUserID() second call = true, want false")
	}

	expiredAt := time.Now().Add(-time.Hour)
	if _, err := database.Conn.Exec(`
		UPDATE licenses
		SET tier = $2, status = $3, expires_at = $4, trial_started_at = $5
		WHERE user_id = $1
	`, user.ID, TierPro, LicenseStatusTrialing, expiredAt, expiredAt.Add(-24*time.Hour)); err != nil {
		t.Fatalf("license update error = %v", err)
	}

	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil || license == nil {
		t.Fatalf("GetLicenseByUserID() error = %v, license = %#v", err, license)
	}
	if license.Tier != TierBasic || license.Status != LicenseStatusActive || license.ExpiresAt != nil {
		t.Fatalf("license after auto-downgrade = %#v", license)
	}
}
