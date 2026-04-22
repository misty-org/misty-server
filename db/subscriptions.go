package db

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

type Tier string

const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
	TierMax  Tier = "max"
)

type Subscription struct {
	UserID        string
	Tier          Tier
	Status        string // active | cancelled | expired
	ExpiresAt     *time.Time
	LicenseDevice string
}

func (db *Database) GetSubscription(userID string) (*Subscription, error) {
	var s Subscription
	var expiresAt sql.NullTime

	err := db.Conn.QueryRow(
		`SELECT user_id, tier, status, expires_at, license_device FROM subscriptions WHERE user_id = $1`,
		userID,
	).Scan(&s.UserID, &s.Tier, &s.Status, &expiresAt, &s.LicenseDevice)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Subscription{UserID: userID, Tier: TierFree, Status: "active"}, nil
		}
		log.Println("Failed to get subscription:", err)
		return nil, err
	}

	if expiresAt.Valid {
		s.ExpiresAt = &expiresAt.Time
	}

	return &s, nil
}

func (db *Database) UpdateLicenseDevice(userID, device string) error {
	_, err := db.Conn.Exec(`
		INSERT INTO subscriptions (user_id, tier, status, license_device)
		VALUES ($1, 'free', 'active', $2)
		ON CONFLICT (user_id) DO UPDATE SET
			license_device = EXCLUDED.license_device,
			updated_at = NOW()
	`, userID, device)
	if err != nil {
		log.Println("Failed to update license device:", err)
	}
	return err
}

func (db *Database) UpsertSubscription(userID string, tier Tier, status string, expiresAt *time.Time) error {
	_, err := db.Conn.Exec(`
		INSERT INTO subscriptions (user_id, tier, status, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			tier = EXCLUDED.tier,
			status = EXCLUDED.status,
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()
	`, userID, tier, status, expiresAt)
	if err != nil {
		log.Println("Failed to upsert subscription:", err)
	}
	return err
}
