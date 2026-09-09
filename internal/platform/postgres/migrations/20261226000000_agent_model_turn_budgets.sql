-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
-- Preserve pinned older workers. New admissions receive the durable budget.
ALTER TABLE space_runs ADD COLUMN model_budget_version INTEGER NOT NULL DEFAULT 0 CHECK(model_budget_version IN (0,1));
ALTER TABLE space_runs ALTER COLUMN model_budget_version SET DEFAULT 1;
ALTER TABLE space_runs ADD COLUMN model_turn_limit INTEGER NOT NULL DEFAULT 20 CHECK(model_turn_limit BETWEEN 1 AND 20);
ALTER TABLE ai_invocations ADD COLUMN model_budget_version INTEGER NOT NULL DEFAULT 0 CHECK(model_budget_version IN (0,1));
ALTER TABLE ai_invocations ALTER COLUMN model_budget_version SET DEFAULT 1;
ALTER TABLE ai_invocations ADD COLUMN model_turn_limit INTEGER NOT NULL DEFAULT 20 CHECK(model_turn_limit BETWEEN 1 AND 20);
CREATE TABLE agent_model_turn_claims (
 run_id TEXT NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 runtime_run_id TEXT NOT NULL,
 node_id TEXT NOT NULL CHECK(length(node_id) BETWEEN 7 AND 200 AND node_id LIKE 'model:%'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 space_run_id TEXT GENERATED ALWAYS AS (CASE WHEN left(run_id,11)='invocation_' THEN NULL ELSE run_id END) STORED REFERENCES space_runs(id) ON DELETE CASCADE,
 ai_invocation_id TEXT GENERATED ALWAYS AS (CASE WHEN left(run_id,11)='invocation_' THEN run_id ELSE NULL END) STORED REFERENCES ai_invocations(id) ON DELETE CASCADE,
 PRIMARY KEY(run_id,node_id)
);
CREATE INDEX agent_model_turn_space_idx ON agent_model_turn_claims(space_run_id) WHERE space_run_id IS NOT NULL;
CREATE INDEX agent_model_turn_invocation_idx ON agent_model_turn_claims(ai_invocation_id) WHERE ai_invocation_id IS NOT NULL;
ALTER TABLE agent_model_turn_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_model_turn_claims FORCE ROW LEVEL SECURITY;
CREATE POLICY owner_policy ON agent_model_turn_claims FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN GRANT SELECT,INSERT ON agent_model_turn_claims TO misty_app; END IF; END $$;
-- +goose StatementEnd
-- +goose Down
-- Do not erase consumed turns or recovery identities during rollback.
SELECT 1;
