-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Personal Agents are creator-scoped companions.  A run snapshots identity and
-- mode, while live authorization always follows the creator's current Space
-- membership.  Existing identities and version history are retained.
ALTER TABLE personal_agents
    ADD COLUMN default_run_mode TEXT NOT NULL DEFAULT 'auto'
        CHECK(default_run_mode IN ('ask','auto','full'));
ALTER TABLE personal_agent_versions
    ADD COLUMN default_run_mode TEXT NOT NULL DEFAULT 'auto'
        CHECK(default_run_mode IN ('ask','auto','full'));

UPDATE personal_agents SET default_run_mode='auto';
UPDATE personal_agent_versions SET default_run_mode='auto';

-- Old permission documents are intentionally discarded rather than archived or
-- dual-read.  The creator's current authority is now the only authority source.
ALTER TABLE personal_agents
    DROP COLUMN IF EXISTS context_permissions,
    DROP COLUMN IF EXISTS tool_permissions;
ALTER TABLE personal_agent_versions
    DROP COLUMN IF EXISTS context_permissions,
    DROP COLUMN IF EXISTS tool_permissions;

UPDATE space_runs
SET state='canceled', error_code='runtime_migrated', error_message='Canceled by the creator-scoped runtime migration',
    runtime_phase='canceled', canceled_at=NOW(), completed_at=NOW(), updated_at=NOW()
WHERE agent_id LIKE 'personal_%'
  AND state IN ('queued','running','cooldown','awaiting_approval');

ALTER TABLE space_runs
    ADD COLUMN owner_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN initial_run_mode TEXT NOT NULL DEFAULT 'auto'
        CHECK(initial_run_mode IN ('ask','auto','full')),
    ADD COLUMN effective_run_mode TEXT NOT NULL DEFAULT 'auto'
        CHECK(effective_run_mode IN ('ask','auto','full')),
    ADD COLUMN agent_version_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK(jsonb_typeof(agent_version_snapshot)='object'),
    ADD COLUMN approval_state TEXT NOT NULL DEFAULT 'none'
        CHECK(approval_state IN ('none','pending','approved','denied','expired')),
    ADD COLUMN parent_run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    ADD COLUMN delegation_depth INTEGER NOT NULL DEFAULT 0 CHECK(delegation_depth BETWEEN 0 AND 2),
    ADD COLUMN context_bindings JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK(jsonb_typeof(context_bindings)='array'),
    ADD COLUMN device_wait_hook_token TEXT NOT NULL DEFAULT '',
    ADD COLUMN device_wait_expires_at TIMESTAMPTZ;

UPDATE space_runs r SET
    owner_user_id=COALESCE(a.owner_user_id,r.requesting_member_id),
    agent_version_snapshot=CASE WHEN a.id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object(
        'id',a.id,'version',a.version,'name',a.name,'instructions',a.instructions,
        'model_id',a.model_id,'reasoning_effort',a.reasoning_effort,'default_run_mode',a.default_run_mode
    ) END,
    initial_run_mode=COALESCE(a.default_run_mode,'auto'),
    effective_run_mode=COALESCE(a.default_run_mode,'auto')
FROM personal_agents a WHERE a.id=r.agent_id;
UPDATE space_runs SET owner_user_id=requesting_member_id WHERE owner_user_id IS NULL;
ALTER TABLE space_runs ALTER COLUMN owner_user_id SET NOT NULL;

CREATE OR REPLACE FUNCTION misty_default_agent_run_owner() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.owner_user_id IS NULL OR NEW.owner_user_id='' THEN
        NEW.owner_user_id := NEW.requesting_member_id;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER space_runs_default_owner BEFORE INSERT ON space_runs
FOR EACH ROW EXECUTE FUNCTION misty_default_agent_run_owner();

ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check CHECK(state IN (
    'queued','running','awaiting_approval','awaiting_device','completed',
    'completed_with_errors','failed','canceled','rejected','cooldown','retrying'
));

-- Generalize the durable queue without losing completed job history.
ALTER TABLE personal_agent_task_run_jobs RENAME TO agent_run_jobs;
ALTER INDEX IF EXISTS personal_agent_task_run_jobs_claim_idx RENAME TO agent_run_jobs_claim_idx;
ALTER INDEX IF EXISTS personal_agent_task_run_jobs_active_agent_idx RENAME TO agent_run_jobs_active_agent_idx;
ALTER TABLE agent_run_jobs ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE agent_run_jobs
    ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'task_assignment'
        CHECK(trigger_kind IN ('task_assignment','direct_instruction','creator_mention','delegated'));

CREATE TABLE agent_run_contexts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('browser_tab','project_root')),
    opaque_ref TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(capabilities)='array'),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metadata)='object'),
    state TEXT NOT NULL DEFAULT 'attached' CHECK(state IN ('attached','detached','expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(run_id,kind,opaque_ref)
);
CREATE INDEX agent_run_contexts_run_idx ON agent_run_contexts(run_id,kind) WHERE state='attached';

CREATE TABLE agent_run_tool_approvals (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    impact TEXT NOT NULL CHECK(impact IN ('routine','consequential','dangerous')),
    arguments_hash TEXT NOT NULL,
    signed_call TEXT NOT NULL,
    hook_token TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved','denied','expired')),
    decided_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW()+INTERVAL '24 hours'),
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(run_id,tool_call_id)
);
CREATE INDEX agent_run_tool_approvals_owner_pending_idx
    ON agent_run_tool_approvals(owner_user_id,created_at) WHERE state='pending';

ALTER TABLE agent_run_contexts ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_run_contexts FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_run_contexts_owner_policy ON agent_run_contexts FOR ALL
    USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
ALTER TABLE agent_run_tool_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_run_tool_approvals FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_run_tool_approvals_owner_policy ON agent_run_tool_approvals FOR ALL
    USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

-- Run-bound contexts supersede broad device grants.  Existing jobs are made
-- terminal before the grant table is removed.
UPDATE workflow_device_node_jobs SET state='canceled',completed_at=NOW()
WHERE state IN ('queued','leased');
ALTER TABLE workflow_device_node_jobs ADD COLUMN context_id TEXT REFERENCES agent_run_contexts(id) ON DELETE SET NULL;
ALTER TABLE workflow_device_node_jobs DROP COLUMN IF EXISTS device_grant_id;

DROP TABLE IF EXISTS personal_agent_member_grants CASCADE;
DROP TABLE IF EXISTS personal_agent_space_grants CASCADE;
DROP TABLE IF EXISTS personal_agent_instances CASCADE;
DROP TABLE IF EXISTS agent_device_grants CASCADE;

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON agent_run_jobs,agent_run_contexts,agent_run_tool_approvals TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- This is an intentionally breaking, one-way authorization migration. Restoring
-- the removed permission hierarchy requires restoring the pre-migration backup.
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
