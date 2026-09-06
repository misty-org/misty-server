-- +goose Up
CREATE TABLE billing.legacy_purchases (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES billing.accounts(user_id),
    license_id TEXT NOT NULL,
    tier_purchased TEXT NOT NULL,
    stripe_checkout_session_id TEXT NOT NULL UNIQUE,
    stripe_payment_intent_id TEXT UNIQUE,
    stripe_customer_id TEXT,
    stripe_charge_id TEXT UNIQUE,
    amount BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'usd',
    status TEXT NOT NULL CHECK(status IN ('completed','refunded','disputed')),
    event_source TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX legacy_purchases_user ON billing.legacy_purchases(user_id);

-- Only the migration role may write checkpoints. Runtime credentials may read
-- them to determine whether missing historical purchase records are meaningful.
CREATE TABLE billing.cutover_checkpoints (
    name TEXT PRIMARY KEY,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    evidence JSONB NOT NULL
);
