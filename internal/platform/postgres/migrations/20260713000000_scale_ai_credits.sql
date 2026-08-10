-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE credit_wallets
    ADD COLUMN IF NOT EXISTS denomination_version SMALLINT NOT NULL DEFAULT 1;

UPDATE credit_wallets
SET monthly_allowance = monthly_allowance * 1000,
    monthly_remaining = monthly_remaining * 1000,
    purchased_remaining = purchased_remaining * 1000,
    reserved_credits = reserved_credits * 1000,
    denomination_version = 2,
    updated_at = NOW()
WHERE denomination_version = 1;

ALTER TABLE credit_wallets
    ALTER COLUMN denomination_version SET DEFAULT 2;

UPDATE credit_reservations
SET reserved_credits = reserved_credits * 1000;

UPDATE credit_ledger
SET monthly_delta = monthly_delta * 1000,
    purchased_delta = purchased_delta * 1000,
    credits_charged = credits_charged * 1000,
    rate_card_version = CASE
        WHEN rate_card_version = '' THEN rate_card_version
        ELSE rate_card_version || '-scaled-1000'
    END;

UPDATE credit_purchases
SET credits = credits * 1000;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

UPDATE credit_purchases
SET credits = GREATEST(1, (credits + 999) / 1000);

UPDATE credit_ledger
SET monthly_delta = monthly_delta / 1000,
    purchased_delta = purchased_delta / 1000,
    credits_charged = credits_charged / 1000,
    rate_card_version = REPLACE(rate_card_version, '-scaled-1000', '');

UPDATE credit_reservations
SET reserved_credits = GREATEST(1, (reserved_credits + 999) / 1000);

UPDATE credit_wallets
SET monthly_allowance = monthly_allowance / 1000,
    monthly_remaining = monthly_remaining / 1000,
    purchased_remaining = purchased_remaining / 1000,
    reserved_credits = reserved_credits / 1000,
    updated_at = NOW();

ALTER TABLE credit_wallets DROP COLUMN IF EXISTS denomination_version;
-- +goose StatementEnd
