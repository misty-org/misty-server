package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SubscriptionStatusTrialing = "trialing"
	SubscriptionStatusActive   = "active"
	SubscriptionStatusPastDue  = "past_due"
)

type StripeSubscription struct {
	ID                   string
	UserID               string
	LicenseID            string
	StripeSubscriptionID string
	StripeCustomerID     string
	StripePriceID        string
	Tier                 Tier
	BillingInterval      string
	Status               string
	CurrentPeriodEnd     *time.Time
	CancelAtPeriodEnd    bool
	CanceledAt           *time.Time
	SourceEventCreatedAt *time.Time
	SourceEventID        string
	LastReconciledAt     *time.Time
	ReconcileAfter       time.Time
	ReconcileFailures    int
	LastReconcileError   string
}

func (db *Database) UpsertStripeSubscription(subscription *StripeSubscription) error {
	_, err := db.upsertStripeSubscription(subscription, false)
	return err
}

// UpsertStripeSubscriptionFromWebhook ignores an older Stripe event rather
// than allowing out-of-order delivery to roll back a newer subscription state.
func (db *Database) UpsertStripeSubscriptionFromWebhook(
	subscription *StripeSubscription,
) (bool, error) {
	return db.upsertStripeSubscription(subscription, true)
}

func (db *Database) upsertStripeSubscription(
	subscription *StripeSubscription,
	rejectOlder bool,
) (bool, error) {
	if subscription.ID == "" {
		subscription.ID = uuid.NewString()
	}
	if subscription.ReconcileAfter.IsZero() {
		subscription.ReconcileAfter = time.Now().UTC()
	}
	applied := false
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		query := `
			INSERT INTO stripe_subscriptions (
				id, user_id, license_id, stripe_subscription_id, stripe_customer_id,
				stripe_price_id, tier, billing_interval, status, current_period_end,
				cancel_at_period_end, canceled_at, source_event_created_at,
				source_event_id, reconcile_after
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT (stripe_subscription_id) DO UPDATE SET
				stripe_customer_id = EXCLUDED.stripe_customer_id,
				stripe_price_id = EXCLUDED.stripe_price_id,
				tier = EXCLUDED.tier,
				billing_interval = EXCLUDED.billing_interval,
				status = EXCLUDED.status,
				current_period_end = EXCLUDED.current_period_end,
				cancel_at_period_end = EXCLUDED.cancel_at_period_end,
				canceled_at = EXCLUDED.canceled_at,
				source_event_created_at = COALESCE(
					EXCLUDED.source_event_created_at,
					stripe_subscriptions.source_event_created_at
				),
				source_event_id = CASE
					WHEN EXCLUDED.source_event_created_at IS NULL
						THEN stripe_subscriptions.source_event_id
					ELSE EXCLUDED.source_event_id
				END,
				reconcile_after = EXCLUDED.reconcile_after,
				updated_at = NOW()
		`
		if rejectOlder {
			query += `
				WHERE stripe_subscriptions.source_event_created_at IS NULL
				   OR EXCLUDED.source_event_created_at IS NULL
				   OR EXCLUDED.source_event_created_at > stripe_subscriptions.source_event_created_at
				   OR (
						EXCLUDED.source_event_created_at = stripe_subscriptions.source_event_created_at
						AND EXCLUDED.source_event_id <> stripe_subscriptions.source_event_id
						AND (
							stripe_subscriptions.status IN ('trialing','active')
							OR EXCLUDED.status NOT IN ('trialing','active')
						)
				   )
			`
		}
		result, err := tx.ExecContext(context.Background(), query,
			subscription.ID, subscription.UserID, subscription.LicenseID,
			subscription.StripeSubscriptionID, subscription.StripeCustomerID,
			subscription.StripePriceID, subscription.Tier, subscription.BillingInterval,
			subscription.Status, subscription.CurrentPeriodEnd,
			subscription.CancelAtPeriodEnd, subscription.CanceledAt,
			subscription.SourceEventCreatedAt, subscription.SourceEventID,
			subscription.ReconcileAfter)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		applied = rows == 1
		return err
	})
	return applied, err
}

func (db *Database) GetStripeSubscriptionByUserID(userID string) (*StripeSubscription, error) {
	return db.getStripeSubscription(
		"user_id",
		userID,
		userRLSSettings(userID),
		`CASE
			WHEN status IN ('trialing','active') THEN 0
			WHEN status = 'past_due' THEN 1
			ELSE 2
		 END, updated_at DESC`,
	)
}

func (db *Database) GetStripeSubscriptionByStripeID(subscriptionID string) (*StripeSubscription, error) {
	return db.getStripeSubscription(
		"stripe_subscription_id",
		subscriptionID,
		TestingServiceRLSSettings(),
		"updated_at DESC",
	)
}

func (db *Database) getStripeSubscription(
	column, value string,
	settings map[string]string,
	orderBy string,
) (*StripeSubscription, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var subscription StripeSubscription
	var periodEnd, canceledAt, sourceEventCreatedAt, lastReconciledAt sql.NullTime
	err := db.TestingWithRLSContext(context.Background(), settings, func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), `
			SELECT id, user_id, license_id, stripe_subscription_id, stripe_customer_id,
			       stripe_price_id, tier, billing_interval, status, current_period_end,
			       cancel_at_period_end, canceled_at,source_event_created_at,
			       source_event_id,last_reconciled_at,reconcile_after,
			       reconcile_failures,last_reconcile_error
			FROM stripe_subscriptions WHERE `+column+` = $1 ORDER BY `+orderBy+` LIMIT 1
		`, value).Scan(&subscription.ID, &subscription.UserID, &subscription.LicenseID,
			&subscription.StripeSubscriptionID, &subscription.StripeCustomerID,
			&subscription.StripePriceID, &subscription.Tier, &subscription.BillingInterval,
			&subscription.Status, &periodEnd, &subscription.CancelAtPeriodEnd, &canceledAt,
			&sourceEventCreatedAt, &subscription.SourceEventID, &lastReconciledAt,
			&subscription.ReconcileAfter, &subscription.ReconcileFailures,
			&subscription.LastReconcileError)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if periodEnd.Valid {
		subscription.CurrentPeriodEnd = &periodEnd.Time
	}
	if canceledAt.Valid {
		subscription.CanceledAt = &canceledAt.Time
	}
	if sourceEventCreatedAt.Valid {
		subscription.SourceEventCreatedAt = &sourceEventCreatedAt.Time
	}
	if lastReconciledAt.Valid {
		subscription.LastReconciledAt = &lastReconciledAt.Time
	}
	return &subscription, nil
}

func (db *Database) GetStripeCustomerIDForUser(userID string) (string, error) {
	subscription, err := db.GetStripeSubscriptionByUserID(userID)
	if err != nil {
		return "", err
	}
	if subscription != nil && subscription.StripeCustomerID != "" {
		return subscription.StripeCustomerID, nil
	}
	var customerID sql.NullString
	err = db.TestingWithRLSContext(context.Background(), userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), `
			SELECT stripe_customer_id FROM stripe_purchases
			WHERE user_id = $1 AND stripe_customer_id IS NOT NULL
			ORDER BY updated_at DESC LIMIT 1
		`, userID).Scan(&customerID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return customerID.String, nil
}

func SubscriptionAllowsPaidAccess(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case SubscriptionStatusTrialing, SubscriptionStatusActive:
		return true
	default:
		return false
	}
}

func (db *Database) ApplyEffectiveSubscriptionEntitlement(subscription *StripeSubscription) error {
	if subscription == nil {
		return nil
	}
	if !SubscriptionAllowsPaidAccess(subscription.Status) {
		effective, err := db.GetStripeSubscriptionByUserID(subscription.UserID)
		if err != nil {
			return err
		}
		if effective != nil && SubscriptionAllowsPaidAccess(effective.Status) {
			subscription = effective
		}
	}
	if strings.EqualFold(subscription.Status, SubscriptionStatusTrialing) {
		return db.SetStripeTrialState(subscription.LicenseID, subscription.Tier, subscription.CurrentPeriodEnd)
	}
	tier := TierBasic
	if SubscriptionAllowsPaidAccess(subscription.Status) {
		tier = NormalizePlan(subscription.Tier)
	} else {
		license, err := db.GetLicenseByUserID(subscription.UserID)
		if err != nil {
			return err
		}
		if license != nil && license.LegacyTier != nil {
			tier = NormalizePlan(*license.LegacyTier)
		}
	}
	if err := db.SetLicenseStateByID(subscription.LicenseID, tier, LicenseStatusActive, nil); err != nil {
		log.Println("Failed to apply effective subscription entitlement:", err)
		return err
	}
	return nil
}
