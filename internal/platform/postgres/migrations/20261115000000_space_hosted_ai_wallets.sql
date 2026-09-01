-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_hosted_ai_wallets (
    space_id TEXT PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
    weekly_allowance_microusd BIGINT NOT NULL DEFAULT 0 CHECK (weekly_allowance_microusd >= 0),
    weekly_remaining_microusd BIGINT NOT NULL DEFAULT 0 CHECK (weekly_remaining_microusd >= 0),
    reserved_microusd BIGINT NOT NULL DEFAULT 0 CHECK (reserved_microusd >= 0),
    reset_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE hosted_ai_reservations
    ADD COLUMN space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL;
ALTER TABLE hosted_ai_usage_ledger
    ADD COLUMN space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL;
ALTER TABLE ai_invocations
    ADD COLUMN space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL;

-- Only backfill invocations whose immutable run link identifies exactly one
-- Space and whose requesting member agrees with the invocation owner. Rows
-- without that canonical proof remain personal/unattributed.
UPDATE ai_invocations invocation
SET space_id=linked.space_id
FROM (
    SELECT invocation.id,MIN(run.space_id) AS space_id
    FROM ai_invocations invocation
    JOIN space_runs run
      ON run.id=invocation.agent_run_id
     AND run.requesting_member_id=invocation.user_id
    WHERE invocation.space_id IS NULL
      AND invocation.state IN ('queued','running','awaiting_approval')
    GROUP BY invocation.id
    HAVING COUNT(DISTINCT run.space_id)=1
) linked
WHERE invocation.id=linked.id;

CREATE INDEX hosted_ai_reservations_space_created_idx
    ON hosted_ai_reservations(space_id, created_at DESC) WHERE space_id IS NOT NULL;
CREATE INDEX hosted_ai_usage_ledger_space_created_idx
    ON hosted_ai_usage_ledger(space_id, created_at DESC) WHERE space_id IS NOT NULL;
CREATE INDEX ai_invocations_space_created_idx
    ON ai_invocations(space_id, created_at DESC) WHERE space_id IS NOT NULL;

ALTER TABLE space_hosted_ai_wallets ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_hosted_ai_wallets FORCE ROW LEVEL SECURITY;
CREATE POLICY space_hosted_ai_wallets_select_policy ON space_hosted_ai_wallets FOR SELECT
    USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_hosted_ai_wallets_service_write_policy ON space_hosted_ai_wallets FOR ALL
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_hosted_ai_wallets TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS hosted_ai_usage_ledger_space_created_idx;
DROP INDEX IF EXISTS hosted_ai_reservations_space_created_idx;
DROP INDEX IF EXISTS ai_invocations_space_created_idx;
ALTER TABLE ai_invocations DROP COLUMN IF EXISTS space_id;
ALTER TABLE hosted_ai_usage_ledger DROP COLUMN IF EXISTS space_id;
ALTER TABLE hosted_ai_reservations DROP COLUMN IF EXISTS space_id;
DROP TABLE IF EXISTS space_hosted_ai_wallets;
-- +goose StatementEnd
