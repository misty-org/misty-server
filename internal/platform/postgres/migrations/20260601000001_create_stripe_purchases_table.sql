-- +goose Up
-- +goose StatementBegin
CREATE TABLE stripe_purchases (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_id TEXT NOT NULL REFERENCES licenses(id) ON DELETE CASCADE,
    tier_purchased TEXT NOT NULL,
    stripe_checkout_session_id TEXT NOT NULL UNIQUE,
    stripe_payment_intent_id TEXT UNIQUE,
    stripe_customer_id TEXT,
    stripe_charge_id TEXT UNIQUE,
    amount BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'usd',
    status TEXT NOT NULL DEFAULT 'completed',
    event_source TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stripe_purchases;
-- +goose StatementEnd
