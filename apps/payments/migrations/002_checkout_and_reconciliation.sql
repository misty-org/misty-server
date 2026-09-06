-- +goose Up
ALTER TABLE billing.subscriptions
  ADD COLUMN reconcile_failures INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN last_reconcile_error TEXT;
CREATE INDEX subscriptions_reconcile_due ON billing.subscriptions(reconcile_after,updated_at)
  WHERE status IN ('trialing','active','past_due');

CREATE TABLE billing.checkout_attempts (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES billing.accounts(user_id),
    license_id TEXT NOT NULL,
    tier TEXT NOT NULL CHECK(tier IN ('pro','max')),
    billing_interval TEXT NOT NULL CHECK(billing_interval IN ('month','year')),
    status TEXT NOT NULL CHECK(status IN ('creating','open','completed','expired','failed')),
    stripe_checkout_session_id TEXT UNIQUE,
    checkout_url TEXT NOT NULL DEFAULT '',
    -- Persist exact Stripe request parameters before any network call so retries
    -- reuse both the idempotency key and its original request body.
    stripe_parameters JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX checkout_attempts_one_open_per_user ON billing.checkout_attempts(user_id)
  WHERE status IN ('creating','open');
CREATE INDEX checkout_attempts_expiry ON billing.checkout_attempts(expires_at)
  WHERE status IN ('creating','open');
