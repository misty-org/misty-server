-- +goose Up
-- This schema is initialized with migration credentials, never API credentials.
-- No FK points into application-owned tables: lifecycle changes arrive through
-- authenticated service contracts and are reconciled explicitly.
CREATE SCHEMA IF NOT EXISTS billing;
REVOKE ALL ON SCHEMA billing FROM PUBLIC;

CREATE TABLE IF NOT EXISTS billing.accounts (
    user_id TEXT PRIMARY KEY,
    license_id TEXT NOT NULL,
    stripe_customer_id TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS billing.webhook_inbox (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    stripe_created_at BIGINT NOT NULL,
    payload JSONB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256)=64),
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','processing','completed','failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_id UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error_code TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS webhook_inbox_due ON billing.webhook_inbox(available_at,received_at)
    WHERE state IN ('pending','processing','failed');

CREATE TABLE IF NOT EXISTS billing.subscriptions (
    stripe_subscription_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES billing.accounts(user_id),
    stripe_customer_id TEXT NOT NULL,
    stripe_price_id TEXT NOT NULL,
    tier TEXT NOT NULL CHECK(tier IN ('pro','max')),
    billing_interval TEXT NOT NULL CHECK(billing_interval IN ('month','year')),
    status TEXT NOT NULL,
    current_period_end TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT false,
    canceled_at TIMESTAMPTZ,
    source_event_id TEXT,
    source_event_created_at BIGINT,
    reconcile_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_reconciled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS billing.entitlement_versions (
    user_id TEXT PRIMARY KEY REFERENCES billing.accounts(user_id),
    revision BIGINT NOT NULL DEFAULT 0 CHECK(revision>=0)
);
CREATE TABLE IF NOT EXISTS billing.entitlement_outbox (
    event_id UUID PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES billing.accounts(user_id),
    revision BIGINT NOT NULL CHECK(revision>0),
    payload JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','processing','delivered','failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_id UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    UNIQUE(user_id,revision)
);
CREATE INDEX IF NOT EXISTS entitlement_outbox_due ON billing.entitlement_outbox(available_at,created_at)
    WHERE state IN ('pending','processing','failed');
