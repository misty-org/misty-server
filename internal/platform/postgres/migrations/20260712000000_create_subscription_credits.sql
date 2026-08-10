-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE licenses ADD COLUMN IF NOT EXISTS legacy_tier TEXT;

UPDATE licenses AS licenses
SET legacy_tier = CASE purchases.tier_purchased
    WHEN 'personal' THEN 'pro'
    WHEN 'pro' THEN 'max'
    WHEN 'max' THEN 'max'
    ELSE NULL
END
FROM (
    SELECT DISTINCT ON (license_id) license_id, tier_purchased
    FROM stripe_purchases
    WHERE status = 'completed'
    ORDER BY license_id, updated_at DESC
) AS purchases
WHERE purchases.license_id = licenses.id;

UPDATE licenses
SET tier = CASE tier
    WHEN 'personal' THEN 'pro'
    WHEN 'pro' THEN 'max'
    ELSE tier
END;

CREATE TABLE stripe_subscriptions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_id TEXT NOT NULL REFERENCES licenses(id) ON DELETE CASCADE,
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    stripe_customer_id TEXT NOT NULL,
    stripe_price_id TEXT NOT NULL DEFAULT '',
    tier TEXT NOT NULL,
    billing_interval TEXT NOT NULL,
    status TEXT NOT NULL,
    current_period_end TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
    canceled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX stripe_subscriptions_one_live_per_user
    ON stripe_subscriptions(user_id)
    WHERE status IN ('trialing', 'active', 'past_due');
CREATE INDEX stripe_subscriptions_customer_id_idx ON stripe_subscriptions(stripe_customer_id);

CREATE TABLE stripe_webhook_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE credit_wallets (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    monthly_allowance BIGINT NOT NULL DEFAULT 0 CHECK (monthly_allowance >= 0),
    monthly_remaining BIGINT NOT NULL DEFAULT 0 CHECK (monthly_remaining >= 0),
    purchased_remaining BIGINT NOT NULL DEFAULT 0 CHECK (purchased_remaining >= 0),
    reserved_credits BIGINT NOT NULL DEFAULT 0 CHECK (reserved_credits >= 0),
    allowance_reset_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE credit_reservations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL UNIQUE,
    meter TEXT NOT NULL,
    reserved_credits BIGINT NOT NULL CHECK (reserved_credits > 0),
    status TEXT NOT NULL DEFAULT 'reserved',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ
);

CREATE TABLE credit_ledger (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reservation_id TEXT REFERENCES credit_reservations(id) ON DELETE SET NULL,
    source TEXT NOT NULL,
    meter TEXT NOT NULL DEFAULT '',
    monthly_delta BIGINT NOT NULL DEFAULT 0,
    purchased_delta BIGINT NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    cached_input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    rate_card_version TEXT NOT NULL DEFAULT '',
    credits_charged BIGINT NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX credit_ledger_user_created_idx ON credit_ledger(user_id, created_at DESC);

CREATE TABLE credit_purchases (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_checkout_session_id TEXT NOT NULL UNIQUE,
    stripe_payment_intent_id TEXT UNIQUE,
    pack_id TEXT NOT NULL,
    credits BIGINT NOT NULL CHECK (credits > 0),
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE stripe_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE stripe_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY stripe_subscriptions_select_policy ON stripe_subscriptions FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY stripe_subscriptions_write_policy ON stripe_subscriptions FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE stripe_webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE stripe_webhook_events FORCE ROW LEVEL SECURITY;
CREATE POLICY stripe_webhook_events_service_policy ON stripe_webhook_events FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE credit_wallets ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_wallets FORCE ROW LEVEL SECURITY;
CREATE POLICY credit_wallets_select_policy ON credit_wallets FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY credit_wallets_write_policy ON credit_wallets FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE credit_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY credit_reservations_select_policy ON credit_reservations FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY credit_reservations_write_policy ON credit_reservations FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE credit_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_ledger FORCE ROW LEVEL SECURITY;
CREATE POLICY credit_ledger_select_policy ON credit_ledger FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY credit_ledger_write_policy ON credit_ledger FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE credit_purchases ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_purchases FORCE ROW LEVEL SECURITY;
CREATE POLICY credit_purchases_select_policy ON credit_purchases FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY credit_purchases_write_policy ON credit_purchases FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS credit_purchases;
DROP TABLE IF EXISTS credit_ledger;
DROP TABLE IF EXISTS credit_reservations;
DROP TABLE IF EXISTS credit_wallets;
DROP TABLE IF EXISTS stripe_webhook_events;
DROP TABLE IF EXISTS stripe_subscriptions;
ALTER TABLE licenses DROP COLUMN IF EXISTS legacy_tier;

UPDATE licenses
SET tier = CASE tier
    WHEN 'pro' THEN 'personal'
    WHEN 'max' THEN 'pro'
    ELSE tier
END;
-- +goose StatementEnd
