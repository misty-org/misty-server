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
)

type Subscription struct {
	UserID    string
	Tier      Tier
	Status    string // active | cancelled | expired
	ExpiresAt *time.Time
}

func (db *Database) GetSubscription(userID string) (*Subscription, error) {
	var s Subscription
	var expiresAt sql.NullTime

	err := db.Conn.QueryRow(
		`SELECT user_id, tier, status, expires_at FROM subscriptions WHERE user_id = $1`,
		userID,
	).Scan(&s.UserID, &s.Tier, &s.Status, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No subscription row — default to free
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
