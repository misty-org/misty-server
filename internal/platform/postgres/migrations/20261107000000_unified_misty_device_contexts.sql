-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

CREATE TABLE ai_invocation_contexts (
    id TEXT PRIMARY KEY,
    invocation_id TEXT NOT NULL REFERENCES ai_invocations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind='browser_tab'),
    opaque_ref TEXT NOT NULL CHECK(char_length(opaque_ref) BETWEEN 1 AND 512),
    display_name TEXT NOT NULL DEFAULT '' CHECK(char_length(display_name)<=255),
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(capabilities)='array'),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metadata)='object'),
    state TEXT NOT NULL DEFAULT 'attached' CHECK(state IN ('attached','detached','expired')),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW()+INTERVAL '30 minutes'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(invocation_id,kind,opaque_ref)
);
CREATE INDEX ai_invocation_contexts_user_active_idx
    ON ai_invocation_contexts(user_id,updated_at DESC)
    WHERE state='attached';

ALTER TABLE ai_invocation_contexts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_invocation_contexts FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_invocation_contexts_owner_policy ON ai_invocation_contexts FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE workflow_device_node_jobs ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE workflow_device_node_jobs
    ADD COLUMN invocation_id TEXT REFERENCES ai_invocations(id) ON DELETE CASCADE,
    ADD COLUMN ai_context_id TEXT REFERENCES ai_invocation_contexts(id) ON DELETE SET NULL;
ALTER TABLE workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_owner_check
    CHECK((run_id IS NOT NULL)::int + (invocation_id IS NOT NULL)::int = 1);
ALTER TABLE workflow_device_node_jobs
    DROP CONSTRAINT IF EXISTS workflow_device_node_jobs_run_id_node_id_attempt_key;
CREATE UNIQUE INDEX workflow_device_node_jobs_run_attempt_idx
    ON workflow_device_node_jobs(run_id,node_id,attempt) WHERE run_id IS NOT NULL;
CREATE UNIQUE INDEX workflow_device_node_jobs_invocation_attempt_idx
    ON workflow_device_node_jobs(invocation_id,node_id,attempt) WHERE invocation_id IS NOT NULL;

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON ai_invocation_contexts TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM workflow_device_node_jobs WHERE invocation_id IS NOT NULL;
DROP INDEX IF EXISTS workflow_device_node_jobs_invocation_attempt_idx;
DROP INDEX IF EXISTS workflow_device_node_jobs_run_attempt_idx;
ALTER TABLE workflow_device_node_jobs DROP CONSTRAINT IF EXISTS workflow_device_node_jobs_owner_check;
ALTER TABLE workflow_device_node_jobs DROP COLUMN IF EXISTS ai_context_id;
ALTER TABLE workflow_device_node_jobs DROP COLUMN IF EXISTS invocation_id;
ALTER TABLE workflow_device_node_jobs ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_run_id_node_id_attempt_key UNIQUE(run_id,node_id,attempt);
DROP TABLE IF EXISTS ai_invocation_contexts;
-- +goose StatementEnd
