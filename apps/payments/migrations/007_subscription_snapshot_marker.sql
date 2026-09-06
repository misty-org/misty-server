-- +goose Up
-- A revision may belong only to a purchase reversal. Record snapshot production
-- separately and retain the marker after delivered outbox rows are pruned.
-- This means queued, never acknowledged or applied by the API.
ALTER TABLE billing.accounts ADD COLUMN subscription_snapshot_enqueued BOOLEAN NOT NULL DEFAULT false;
UPDATE billing.accounts a SET subscription_snapshot_enqueued=true
WHERE EXISTS(SELECT 1 FROM billing.entitlement_outbox e WHERE e.user_id=a.user_id AND e.payload ? 'subscription');
