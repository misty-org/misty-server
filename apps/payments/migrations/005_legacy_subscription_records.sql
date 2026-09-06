-- +goose Up
-- Operator-owned original records preserve Go-only IDs/timestamps and prove the
-- exact source used by the one-time subscription/customer ownership handover.
CREATE TABLE billing.legacy_subscription_records (
    stripe_subscription_id TEXT PRIMARY KEY,
    source_record JSONB NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
