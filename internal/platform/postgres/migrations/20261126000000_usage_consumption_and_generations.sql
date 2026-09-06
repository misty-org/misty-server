-- +goose Up
SELECT set_config('app.rls_mode','service',true);
ALTER TABLE hosted_ai_wallets ADD COLUMN weekly_consumed_microusd BIGINT NOT NULL DEFAULT 0 CHECK(weekly_consumed_microusd>=0);
ALTER TABLE space_hosted_ai_wallets ADD COLUMN weekly_consumed_microusd BIGINT NOT NULL DEFAULT 0 CHECK(weekly_consumed_microusd>=0);
-- Preserve the current balance. Historical consumption already lost by an old
-- clamped downgrade cannot be inferred safely from Space refund ledger rows.
UPDATE hosted_ai_wallets SET weekly_consumed_microusd=GREATEST(0,weekly_allowance_microusd-weekly_remaining_microusd);
UPDATE space_hosted_ai_wallets SET weekly_consumed_microusd=GREATEST(0,weekly_allowance_microusd-weekly_remaining_microusd);
ALTER TABLE hosted_ai_reservations ADD COLUMN generation INTEGER NOT NULL DEFAULT 1 CHECK(generation>0);
ALTER TABLE hosted_ai_reservations ADD COLUMN requested_microusd BIGINT CHECK(requested_microusd>0);
ALTER TABLE hosted_ai_reservations ADD COLUMN allow_partial BOOLEAN;
ALTER TABLE hosted_ai_usage_ledger ADD COLUMN personal_reset_at TIMESTAMPTZ;
ALTER TABLE hosted_ai_usage_ledger ADD COLUMN space_reset_at TIMESTAMPTZ;
ALTER TABLE hosted_ai_usage_ledger ADD COLUMN space_delta_microusd BIGINT NOT NULL DEFAULT 0;
ALTER TABLE hosted_ai_usage_ledger ADD COLUMN request_sha256 TEXT CHECK(request_sha256 IS NULL OR length(request_sha256)=64);

-- +goose Down
ALTER TABLE hosted_ai_usage_ledger DROP COLUMN request_sha256,DROP COLUMN space_delta_microusd,DROP COLUMN space_reset_at,DROP COLUMN personal_reset_at;
ALTER TABLE hosted_ai_reservations DROP COLUMN allow_partial,DROP COLUMN requested_microusd,DROP COLUMN generation;
ALTER TABLE space_hosted_ai_wallets DROP COLUMN weekly_consumed_microusd;
ALTER TABLE hosted_ai_wallets DROP COLUMN weekly_consumed_microusd;
