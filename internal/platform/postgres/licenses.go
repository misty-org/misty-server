package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

type Tier string

const (
	TierBasic Tier = "basic"
	// TierPersonal is retained only for interpreting historical purchases.
	TierPersonal Tier = "personal"
	TierPro      Tier = "pro"
	TierMax      Tier = "max"
)

const (
	LicenseStatusActive   = "active"
	LicenseStatusTrialing = "trialing"
)

type License struct {
	ID             string
	UserID         string
	Tier           Tier
	Status         string
	ExpiresAt      *time.Time
	TrialStartedAt *time.Time
	LicenseDevice  string
	LegacyTier     *Tier
}

func createLicenseTx(tx *sql.Tx, licenseID string, userID string, tier Tier, status string, expiresAt *time.Time) (*License, error) {
	license := &License{
		ID:             licenseID,
		UserID:         userID,
		Tier:           tier,
		Status:         status,
		ExpiresAt:      expiresAt,
		TrialStartedAt: nil,
	}

	_, err := tx.ExecContext(
		context.Background(),
		`INSERT INTO licenses (id, user_id, tier, status, expires_at, trial_started_at, license_device) VALUES ($1, $2, $3, $4, $5, $6, '')`,
		license.ID, license.UserID, license.Tier, license.Status, license.ExpiresAt, license.TrialStartedAt,
	)
	if err != nil {
		return nil, err
	}

	return license, nil
}

func (db *Database) GetLicenseByUserID(userID string) (*License, error) {
	var lic License
	var expiresAt sql.NullTime
	var trialStartedAt sql.NullTime
	var legacyTier sql.NullString

	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT id, user_id, tier, status, expires_at, trial_started_at, license_device, legacy_tier FROM licenses WHERE user_id = $1`,
			userID,
		).Scan(&lic.ID, &lic.UserID, &lic.Tier, &lic.Status, &expiresAt, &trialStartedAt, &lic.LicenseDevice, &legacyTier)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		log.Println("Failed to get license:", err)
		return nil, err
	}

	if expiresAt.Valid {
		lic.ExpiresAt = &expiresAt.Time
	}
	if trialStartedAt.Valid {
		lic.TrialStartedAt = &trialStartedAt.Time
	}
	if legacyTier.Valid {
		value := Tier(legacyTier.String)
		lic.LegacyTier = &value
	}

	return &lic, nil
}

func (db *Database) UpdateLicenseDevice(userID, device string) error {
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
			UPDATE licenses
			SET license_device = $2,
				updated_at = NOW()
			WHERE user_id = $1
		`, userID, device)
		return err
	})
	if err != nil {
		log.Println("Failed to update license device:", err)
	}
	return err
}
