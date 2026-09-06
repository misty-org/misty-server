-- +goose Up
-- A closure record is also the permanent billing-admission tombstone. It must
-- survive provider retries, delayed webhooks and application-account anonymizing.
CREATE TABLE billing.account_closures (
    user_id TEXT PRIMARY KEY REFERENCES billing.accounts(user_id),
    license_id TEXT NOT NULL,
    deletion_request_id TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL DEFAULT 'closing' CHECK (state IN ('closing','closed')),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CHECK ((state='closed')=(completed_at IS NOT NULL))
);
CREATE INDEX account_closures_pending ON billing.account_closures(updated_at,user_id) WHERE state='closing';
-- +goose Down
-- Forward only: removing a tombstone could reopen a deleted account's billing.
SELECT 1;
