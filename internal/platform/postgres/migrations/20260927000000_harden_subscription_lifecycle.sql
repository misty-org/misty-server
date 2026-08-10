-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE stripe_subscriptions
    ADD COLUMN source_event_created_at TIMESTAMPTZ,
    ADD COLUMN source_event_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_reconciled_at TIMESTAMPTZ,
    ADD COLUMN reconcile_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN reconcile_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_reconcile_error TEXT NOT NULL DEFAULT '';

CREATE INDEX stripe_subscriptions_reconcile_due_idx
    ON stripe_subscriptions(reconcile_after, updated_at)
    WHERE status IN ('trialing', 'active', 'past_due');

CREATE TABLE stripe_subscription_checkout_attempts (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_id TEXT NOT NULL REFERENCES licenses(id) ON DELETE CASCADE,
    tier TEXT NOT NULL CHECK (tier IN ('pro', 'max')),
    billing_interval TEXT NOT NULL CHECK (billing_interval IN ('month', 'year')),
    status TEXT NOT NULL CHECK (status IN ('creating', 'open', 'completed', 'expired', 'failed')),
    stripe_checkout_session_id TEXT UNIQUE,
    checkout_url TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX stripe_subscription_checkout_attempts_one_open_per_user
    ON stripe_subscription_checkout_attempts(user_id)
    WHERE status IN ('creating', 'open');

CREATE INDEX stripe_subscription_checkout_attempts_expiry_idx
    ON stripe_subscription_checkout_attempts(expires_at)
    WHERE status IN ('creating', 'open');

ALTER TABLE stripe_subscription_checkout_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE stripe_subscription_checkout_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY stripe_subscription_checkout_attempts_select_policy
    ON stripe_subscription_checkout_attempts FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY stripe_subscription_checkout_attempts_write_policy
    ON stripe_subscription_checkout_attempts FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS stripe_subscription_checkout_attempts;

DROP INDEX IF EXISTS stripe_subscriptions_reconcile_due_idx;
ALTER TABLE stripe_subscriptions
    DROP COLUMN IF EXISTS last_reconcile_error,
    DROP COLUMN IF EXISTS reconcile_failures,
    DROP COLUMN IF EXISTS reconcile_after,
    DROP COLUMN IF EXISTS last_reconciled_at,
    DROP COLUMN IF EXISTS source_event_id,
    DROP COLUMN IF EXISTS source_event_created_at;
-- +goose StatementEnd
