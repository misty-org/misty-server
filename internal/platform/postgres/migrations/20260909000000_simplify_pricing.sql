-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM credit_purchases
        WHERE status='completed' AND stripe_checkout_session_id NOT LIKE 'cs_test_%'
    ) THEN
        RAISE EXCEPTION 'pricing migration blocked: completed credit purchases require manual handling';
    END IF;
END
$$;

UPDATE licenses SET tier='pro' WHERE tier='max';
UPDATE stripe_subscriptions SET tier='pro' WHERE tier='max';
UPDATE smart_library_folders SET included_images=0,billable_images=eligible_images;

ALTER TABLE credit_wallets RENAME TO hosted_ai_wallets;
ALTER TABLE hosted_ai_wallets RENAME COLUMN monthly_allowance TO weekly_allowance_microusd;
ALTER TABLE hosted_ai_wallets RENAME COLUMN monthly_remaining TO weekly_remaining_microusd;
ALTER TABLE hosted_ai_wallets RENAME COLUMN reserved_credits TO reserved_microusd;
ALTER TABLE hosted_ai_wallets RENAME COLUMN allowance_reset_at TO reset_at;
UPDATE hosted_ai_wallets w SET
    weekly_allowance_microusd=CASE WHEN l.tier IN ('pro','max') THEN 1000000 ELSE 150000 END,
    weekly_remaining_microusd=CASE WHEN l.tier IN ('pro','max') THEN 1000000 ELSE 150000 END,
    reserved_microusd=0,
    reset_at=(date_trunc('week',NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')+INTERVAL '7 days'
FROM licenses l WHERE l.user_id=w.user_id;
ALTER TABLE hosted_ai_wallets DROP COLUMN purchased_remaining;

ALTER TABLE credit_reservations RENAME TO hosted_ai_reservations;
ALTER TABLE hosted_ai_reservations RENAME COLUMN reserved_credits TO reserved_microusd;
UPDATE hosted_ai_reservations SET status='released',settled_at=NOW() WHERE status='reserved';

ALTER TABLE credit_ledger RENAME TO hosted_ai_usage_ledger;
ALTER TABLE hosted_ai_usage_ledger RENAME COLUMN monthly_delta TO weekly_delta_microusd;
ALTER TABLE hosted_ai_usage_ledger RENAME COLUMN credits_charged TO charged_microusd;
ALTER TABLE hosted_ai_usage_ledger DROP COLUMN purchased_delta;
ALTER TABLE hosted_ai_usage_ledger ADD COLUMN provider_cost_microusd BIGINT NOT NULL DEFAULT 0;
DROP INDEX IF EXISTS credit_ledger_user_created_idx;
CREATE INDEX hosted_ai_usage_ledger_user_created_idx ON hosted_ai_usage_ledger(user_id,created_at DESC);

DROP TABLE credit_purchases;

ALTER TABLE space_rendition_reservations DROP CONSTRAINT space_rendition_reservations_source_kind_check;
ALTER TABLE space_rendition_reservations ADD CONSTRAINT space_rendition_reservations_source_kind_check
    CHECK (source_kind IN ('edit','export','preview'));

CREATE TABLE owner_storage_usage (
    owner_user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    reserved_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    over_quota_since TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO owner_storage_usage(owner_user_id,used_bytes,reserved_bytes)
SELECT u.id,COALESCE(SUM(su.used_bytes),0),COALESCE(SUM(su.reserved_bytes),0)
FROM users u LEFT JOIN spaces s ON s.owner_user_id=u.id AND s.lifecycle_state='active'
LEFT JOIN space_storage_usage su ON su.space_id=s.id
GROUP BY u.id;

CREATE OR REPLACE FUNCTION refresh_owner_storage_usage(candidate_owner TEXT) RETURNS VOID AS $$
BEGIN
    INSERT INTO owner_storage_usage(owner_user_id,used_bytes,reserved_bytes,version,updated_at)
    SELECT candidate_owner,
        COALESCE((SELECT SUM(su.used_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active'),0),
        COALESCE((SELECT SUM(su.reserved_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active'),0),
        1,NOW()
    ON CONFLICT(owner_user_id) DO UPDATE SET
        used_bytes=EXCLUDED.used_bytes,
        reserved_bytes=EXCLUDED.reserved_bytes,
        version=owner_storage_usage.version+1,
        updated_at=NOW();
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sync_owner_storage_from_space_usage() RETURNS TRIGGER AS $$
DECLARE candidate_space TEXT; candidate_owner TEXT;
BEGIN
    candidate_space := COALESCE(NEW.space_id,OLD.space_id);
    SELECT owner_user_id INTO candidate_owner FROM spaces WHERE id=candidate_space;
    IF candidate_owner IS NOT NULL THEN PERFORM refresh_owner_storage_usage(candidate_owner); END IF;
    RETURN COALESCE(NEW,OLD);
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER space_usage_owner_pool_sync
AFTER INSERT OR UPDATE OR DELETE ON space_storage_usage
FOR EACH ROW EXECUTE FUNCTION sync_owner_storage_from_space_usage();

CREATE OR REPLACE FUNCTION sync_owner_storage_after_transfer() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.owner_user_id IS DISTINCT FROM NEW.owner_user_id THEN
        PERFORM refresh_owner_storage_usage(OLD.owner_user_id);
        PERFORM refresh_owner_storage_usage(NEW.owner_user_id);
    ELSIF OLD.lifecycle_state IS DISTINCT FROM NEW.lifecycle_state THEN
        PERFORM refresh_owner_storage_usage(NEW.owner_user_id);
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER spaces_owner_pool_sync
AFTER UPDATE OF owner_user_id,lifecycle_state ON spaces
FOR EACH ROW EXECUTE FUNCTION sync_owner_storage_after_transfer();

ALTER TABLE owner_storage_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE owner_storage_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY owner_storage_usage_policy ON owner_storage_usage FOR ALL
    USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON owner_storage_usage TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS spaces_owner_pool_sync ON spaces;
DROP FUNCTION IF EXISTS sync_owner_storage_after_transfer();
DROP TRIGGER IF EXISTS space_usage_owner_pool_sync ON space_storage_usage;
DROP FUNCTION IF EXISTS sync_owner_storage_from_space_usage();
DROP FUNCTION IF EXISTS refresh_owner_storage_usage(TEXT);
DROP TABLE IF EXISTS owner_storage_usage;

ALTER TABLE space_rendition_reservations DROP CONSTRAINT space_rendition_reservations_source_kind_check;
ALTER TABLE space_rendition_reservations ADD CONSTRAINT space_rendition_reservations_source_kind_check
    CHECK (source_kind IN ('edit','export'));

CREATE TABLE credit_purchases (
    id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_checkout_session_id TEXT NOT NULL UNIQUE,stripe_payment_intent_id TEXT UNIQUE,
    pack_id TEXT NOT NULL,credits BIGINT NOT NULL CHECK (credits>0),status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE credit_purchases ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_purchases FORCE ROW LEVEL SECURITY;
CREATE POLICY credit_purchases_select_policy ON credit_purchases FOR SELECT USING (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY credit_purchases_write_policy ON credit_purchases FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DROP INDEX IF EXISTS hosted_ai_usage_ledger_user_created_idx;
ALTER TABLE hosted_ai_usage_ledger DROP COLUMN provider_cost_microusd;
ALTER TABLE hosted_ai_usage_ledger ADD COLUMN purchased_delta BIGINT NOT NULL DEFAULT 0;
ALTER TABLE hosted_ai_usage_ledger RENAME COLUMN charged_microusd TO credits_charged;
ALTER TABLE hosted_ai_usage_ledger RENAME COLUMN weekly_delta_microusd TO monthly_delta;
ALTER TABLE hosted_ai_usage_ledger RENAME TO credit_ledger;
CREATE INDEX credit_ledger_user_created_idx ON credit_ledger(user_id,created_at DESC);

ALTER TABLE hosted_ai_reservations RENAME COLUMN reserved_microusd TO reserved_credits;
ALTER TABLE hosted_ai_reservations RENAME TO credit_reservations;

ALTER TABLE hosted_ai_wallets ADD COLUMN purchased_remaining BIGINT NOT NULL DEFAULT 0;
ALTER TABLE hosted_ai_wallets RENAME COLUMN reset_at TO allowance_reset_at;
ALTER TABLE hosted_ai_wallets RENAME COLUMN reserved_microusd TO reserved_credits;
ALTER TABLE hosted_ai_wallets RENAME COLUMN weekly_remaining_microusd TO monthly_remaining;
ALTER TABLE hosted_ai_wallets RENAME COLUMN weekly_allowance_microusd TO monthly_allowance;
ALTER TABLE hosted_ai_wallets RENAME TO credit_wallets;
-- +goose StatementEnd
