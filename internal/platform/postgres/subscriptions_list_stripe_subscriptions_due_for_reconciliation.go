package db

import (
	"context"
	"database/sql"
	"time"
)

func (db *Database) ListStripeSubscriptionsDueForReconciliation(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]StripeSubscription, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var subscriptions []StripeSubscription
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id,user_id,license_id,stripe_subscription_id,stripe_customer_id,
			       stripe_price_id,tier,billing_interval,status,current_period_end,
			       cancel_at_period_end,canceled_at,source_event_created_at,
			       source_event_id,last_reconciled_at,reconcile_after,
			       reconcile_failures,last_reconcile_error
			FROM stripe_subscriptions
			WHERE status IN ('trialing','active','past_due')
			  AND reconcile_after<=$1
			ORDER BY reconcile_after,id
			LIMIT $2
		`, now.UTC(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var subscription StripeSubscription
			var periodEnd, canceledAt, sourceCreated, reconciled sql.NullTime
			if err := rows.Scan(
				&subscription.ID,
				&subscription.UserID,
				&subscription.LicenseID,
				&subscription.StripeSubscriptionID,
				&subscription.StripeCustomerID,
				&subscription.StripePriceID,
				&subscription.Tier,
				&subscription.BillingInterval,
				&subscription.Status,
				&periodEnd,
				&subscription.CancelAtPeriodEnd,
				&canceledAt,
				&sourceCreated,
				&subscription.SourceEventID,
				&reconciled,
				&subscription.ReconcileAfter,
				&subscription.ReconcileFailures,
				&subscription.LastReconcileError,
			); err != nil {
				return err
			}
			if periodEnd.Valid {
				subscription.CurrentPeriodEnd = &periodEnd.Time
			}
			if canceledAt.Valid {
				subscription.CanceledAt = &canceledAt.Time
			}
			if sourceCreated.Valid {
				subscription.SourceEventCreatedAt = &sourceCreated.Time
			}
			if reconciled.Valid {
				subscription.LastReconciledAt = &reconciled.Time
			}
			subscriptions = append(subscriptions, subscription)
		}
		return rows.Err()
	})
	return subscriptions, err
}

func (db *Database) MarkStripeSubscriptionReconciled(
	ctx context.Context,
	subscriptionID string,
	now, reconcileAfter time.Time,
) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE stripe_subscriptions
			SET last_reconciled_at=$2,reconcile_after=$3,reconcile_failures=0,
			    last_reconcile_error='',updated_at=NOW()
			WHERE stripe_subscription_id=$1
		`, subscriptionID, now.UTC(), reconcileAfter.UTC())
		return err
	})
}

func (db *Database) MarkStripeSubscriptionReconcileFailed(
	ctx context.Context,
	subscriptionID, failure string,
	reconcileAfter time.Time,
) error {
	if len(failure) > 500 {
		failure = failure[:500]
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE stripe_subscriptions
			SET reconcile_after=$2,reconcile_failures=reconcile_failures+1,
			    last_reconcile_error=$3,updated_at=NOW()
			WHERE stripe_subscription_id=$1
		`, subscriptionID, reconcileAfter.UTC(), failure)
		return err
	})
}

// ExpireStaleSubscriptionEntitlements is the bounded fail-safe for a missed
// webhook plus repeated Stripe API failures. It does not mutate the Stripe
// status, so checkout remains blocked and a later successful reconciliation
// can restore the canonical paid tier without creating another subscription.
func (db *Database) ExpireStaleSubscriptionEntitlements(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	affected := int64(0)
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			WITH stale AS (
				SELECT s.license_id
				FROM stripe_subscriptions s
				WHERE s.status IN ('trialing','active')
				  AND s.current_period_end IS NOT NULL
				  AND s.current_period_end<=$1
				ORDER BY s.current_period_end,s.id
				LIMIT $2
			)
			UPDATE licenses l
			SET tier=COALESCE(NULLIF(l.legacy_tier,''),'basic'),
			    status='active',expires_at=NULL,updated_at=NOW()
			FROM stale
			WHERE l.id=stale.license_id
			  AND l.tier IN ('pro','max','personal')
		`, cutoff.UTC(), limit)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		return err
	})
	return int(affected), err
}

func (db *Database) StripeEventProcessed(eventID string) (bool, error) {
	var exists bool
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT EXISTS(SELECT 1 FROM stripe_webhook_events WHERE event_id = $1)`, eventID).Scan(&exists)
	})
	return exists, err
}

func (db *Database) MarkStripeEventProcessed(eventID, eventType string) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
			INSERT INTO stripe_webhook_events (event_id, event_type) VALUES ($1, $2)
			ON CONFLICT (event_id) DO NOTHING
		`, eventID, eventType)
		return err
	})
}
