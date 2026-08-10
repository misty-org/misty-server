package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type SubscriptionCheckoutAttempt struct {
	ID                      string
	UserID                  string
	LicenseID               string
	Tier                    Tier
	BillingInterval         string
	Status                  string
	StripeCheckoutSessionID string
	CheckoutURL             string
	ExpiresAt               time.Time
}

// BeginSubscriptionCheckout returns the one active checkout attempt for a user,
// creating it when necessary. The partial unique index is the final guard
// against concurrent requests creating separate Stripe Checkout sessions.
func (db *Database) BeginSubscriptionCheckout(
	ctx context.Context,
	userID, licenseID string,
	tier Tier,
	interval string,
	now time.Time,
	lifetime time.Duration,
) (*SubscriptionCheckoutAttempt, bool, error) {
	if lifetime < 30*time.Minute {
		lifetime = 35 * time.Minute
	}
	now = now.UTC()
	var attempt SubscriptionCheckoutAttempt
	created := false
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		attempt = SubscriptionCheckoutAttempt{
			ID: uuid.NewString(), UserID: userID, LicenseID: licenseID,
			Tier: tier, BillingInterval: interval, Status: "creating",
			ExpiresAt: now.Add(lifetime),
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO stripe_subscription_checkout_attempts
				(id,user_id,license_id,tier,billing_interval,status,expires_at)
			VALUES($1,$2,$3,$4,$5,'creating',$6)
			ON CONFLICT DO NOTHING
		`, attempt.ID, userID, licenseID, tier, interval, attempt.ExpiresAt)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 1 {
			created = true
			return nil
		}
		return scanSubscriptionCheckoutAttempt(tx.QueryRowContext(ctx, `
			SELECT id,user_id,license_id,tier,billing_interval,status,
			       COALESCE(stripe_checkout_session_id,''),checkout_url,expires_at
			FROM stripe_subscription_checkout_attempts
			WHERE user_id=$1 AND status IN ('creating','open')
			ORDER BY created_at DESC LIMIT 1
		`, userID), &attempt)
	})
	return &attempt, created, err
}

func (db *Database) OpenSubscriptionCheckout(
	ctx context.Context,
	attemptID, sessionID, checkoutURL string,
	expiresAt time.Time,
) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE stripe_subscription_checkout_attempts
			SET status='open',stripe_checkout_session_id=$2,checkout_url=$3,
			    expires_at=$4,updated_at=NOW()
			WHERE id=$1 AND status IN ('creating','open')
		`, attemptID, sessionID, checkoutURL, expiresAt.UTC())
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("subscription checkout attempt is no longer active")
		}
		return nil
	})
}

func (db *Database) FailSubscriptionCheckout(ctx context.Context, attemptID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE stripe_subscription_checkout_attempts
			SET status='failed',updated_at=NOW()
			WHERE id=$1 AND status='creating'
		`, attemptID)
		return err
	})
}

func (db *Database) CompleteSubscriptionCheckoutBySessionID(
	ctx context.Context,
	sessionID string,
) error {
	if sessionID == "" {
		return nil
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE stripe_subscription_checkout_attempts
			SET status='completed',updated_at=NOW()
			WHERE stripe_checkout_session_id=$1 AND status IN ('creating','open','expired')
		`, sessionID)
		return err
	})
}

func (db *Database) ExpireSubscriptionCheckoutBySessionID(
	ctx context.Context,
	sessionID string,
) error {
	if sessionID == "" {
		return nil
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE stripe_subscription_checkout_attempts
			SET status='expired',updated_at=NOW()
			WHERE stripe_checkout_session_id=$1 AND status IN ('creating','open')
		`, sessionID)
		return err
	})
}

func (db *Database) HasCompletedSubscriptionCheckoutWithoutSubscription(
	ctx context.Context,
	userID string,
) (bool, error) {
	var exists bool
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM stripe_subscription_checkout_attempts attempts
				WHERE attempts.user_id=$1 AND attempts.status='completed'
				  AND NOT EXISTS(
					SELECT 1 FROM stripe_subscriptions subscriptions
					WHERE subscriptions.user_id=attempts.user_id
				  )
			)
		`, userID).Scan(&exists)
	})
	return exists, err
}

func scanSubscriptionCheckoutAttempt(
	row *sql.Row,
	attempt *SubscriptionCheckoutAttempt,
) error {
	return row.Scan(
		&attempt.ID,
		&attempt.UserID,
		&attempt.LicenseID,
		&attempt.Tier,
		&attempt.BillingInterval,
		&attempt.Status,
		&attempt.StripeCheckoutSessionID,
		&attempt.CheckoutURL,
		&attempt.ExpiresAt,
	)
}
