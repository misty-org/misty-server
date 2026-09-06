-- +goose Up
-- Original Go records remain operator-owned; recovery never fabricates a native
-- create request or replays an old Stripe idempotency key.
CREATE TABLE billing.legacy_checkout_records (
    id TEXT PRIMARY KEY,
    source_record JSONB NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE billing.legacy_checkout_recovery (
    id TEXT PRIMARY KEY REFERENCES billing.legacy_checkout_records(id),
    user_id TEXT NOT NULL REFERENCES billing.accounts(user_id),
    license_id TEXT NOT NULL,
    tier TEXT NOT NULL CHECK(tier IN ('pro','max')),
    billing_interval TEXT NOT NULL CHECK(billing_interval IN ('month','year')),
    source_status TEXT NOT NULL CHECK(source_status IN ('creating','open','completed','expired','failed')),
    source_session_id TEXT,
    source_created_at TIMESTAMPTZ NOT NULL,
    source_expires_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','verified','review')),
    status TEXT CHECK(status IN ('open','completed','expired','absent')),
    stripe_checkout_session_id TEXT UNIQUE,
    checkout_url TEXT NOT NULL DEFAULT '',
    session_expires_at TIMESTAMPTZ,
    recovery_cursor TEXT,
    recovery_pages INTEGER NOT NULL DEFAULT 0 CHECK(recovery_pages>=0),
    candidate_session_id TEXT,
    recovery_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    recovery_failures INTEGER NOT NULL DEFAULT 0,
    last_recovery_error TEXT,
    verified_at TIMESTAMPTZ
);
CREATE INDEX legacy_checkout_recovery_account ON billing.legacy_checkout_recovery(user_id);
CREATE INDEX legacy_checkout_recovery_due ON billing.legacy_checkout_recovery(recovery_after,source_created_at,id)
    WHERE state='pending' OR (state='verified' AND status='open');
