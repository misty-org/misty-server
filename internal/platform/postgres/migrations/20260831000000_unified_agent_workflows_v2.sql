-- +goose Up
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';

-- Preserve valid shared Space Agents, their workflow definitions, member
-- conversations, personal run history, connections, and trusted devices.
-- Standalone workflow runs cannot exist in v2 because only Agents execute
-- workflows, so only those known legacy rows are removed.
DELETE FROM space_runs WHERE resource_kind='workflow';

-- The old device/folder Agent runtime has no route or compatibility layer in
-- v2. Drop only its known namespace after the preceding migration has already
-- materialized valid definitions as shared space_agents/workflows.
DROP TABLE IF EXISTS agent_artifacts,agent_attachments,agent_approvals,
    agent_job_events,agent_jobs,agent_triggers,agent_members,
    agent_conversation_events,agent_conversations,agent_definitions CASCADE;

ALTER TABLE space_agents ALTER COLUMN active_workflow_version_id DROP NOT NULL;
ALTER TABLE space_agents ADD COLUMN access_policy JSONB NOT NULL DEFAULT '{"mode":"space","allowedUserIds":[]}'::jsonb
    CHECK (jsonb_typeof(access_policy)='object');
ALTER TABLE space_agents ADD COLUMN published_agent_version_id TEXT;

CREATE TABLE space_agent_versions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES space_agents(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    creator_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version INTEGER NOT NULL CHECK(version > 0),
    name TEXT NOT NULL CHECK(char_length(name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT 'bot',
    instructions TEXT NOT NULL DEFAULT '',
    access_policy JSONB NOT NULL CHECK(jsonb_typeof(access_policy)='object'),
    checksum_sha256 TEXT NOT NULL CHECK(checksum_sha256 ~ '^[0-9a-f]{64}$'),
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_id,version),
    UNIQUE(agent_id,checksum_sha256)
);
ALTER TABLE space_agents ADD CONSTRAINT space_agents_published_version_fkey
    FOREIGN KEY(published_agent_version_id) REFERENCES space_agent_versions(id) ON DELETE SET NULL;

CREATE TABLE space_agent_version_workflows (
    agent_version_id TEXT NOT NULL REFERENCES space_agent_versions(id) ON DELETE CASCADE,
    workflow_version_id TEXT NOT NULL REFERENCES space_workflow_versions(id) ON DELETE RESTRICT,
    alias TEXT NOT NULL CHECK(alias ~ '^[A-Za-z][A-Za-z0-9_-]{0,79}$'),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(agent_version_id,workflow_version_id),
    UNIQUE(agent_version_id,alias)
);

-- Existing valid Space Agents remain usable. Their current shared definition
-- becomes immutable version 1 and their active workflow is pinned to it.
INSERT INTO space_agent_versions(
    id,agent_id,space_id,creator_user_id,version,name,description,icon,
    instructions,access_policy,checksum_sha256,published_at
)
SELECT
    'agentver_' || md5(a.id || ':1'),a.id,a.space_id,a.creator_user_id,1,
    a.name,a.description,a.icon,a.instructions,a.access_policy,
    md5(a.id || ':' || a.version::text || ':' || a.instructions) ||
        md5(a.name || ':' || COALESCE(a.active_workflow_version_id,'')),
    a.updated_at
FROM space_agents a
ON CONFLICT(agent_id,version) DO NOTHING;

INSERT INTO space_agent_version_workflows(agent_version_id,workflow_version_id,alias,enabled,position)
SELECT v.id,a.active_workflow_version_id,'primary',TRUE,0
FROM space_agents a
JOIN space_agent_versions v ON v.agent_id=a.id AND v.version=1
WHERE a.active_workflow_version_id IS NOT NULL
ON CONFLICT DO NOTHING;

UPDATE space_agents a SET published_agent_version_id=v.id
FROM space_agent_versions v
WHERE v.agent_id=a.id AND v.version=1 AND a.published_agent_version_id IS NULL;

CREATE TABLE space_agent_instances (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES space_agents(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_version_id TEXT NOT NULL REFERENCES space_agent_versions(id) ON DELETE RESTRICT,
    connection_bindings JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(connection_bindings)='object'),
    capability_grants JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(capability_grants)='array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_id,user_id)
);
CREATE INDEX space_agent_instances_user_idx ON space_agent_instances(user_id,updated_at DESC);

CREATE TABLE space_agent_instance_workflows (
    instance_id TEXT NOT NULL REFERENCES space_agent_instances(id) ON DELETE CASCADE,
    workflow_version_id TEXT NOT NULL REFERENCES space_workflow_versions(id) ON DELETE RESTRICT,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    trigger_config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(trigger_config)='object'),
    consent JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(consent)='object'),
    cursor JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(cursor)='object'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(instance_id,workflow_version_id)
);

CREATE TABLE space_agent_memory_events (
    id BIGSERIAL PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES space_agent_instances(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('user','agent','tool','workflow','compaction')),
    data JSONB NOT NULL CHECK(jsonb_typeof(data)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_agent_memory_events_instance_idx ON space_agent_memory_events(instance_id,id);

ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_resource_kind_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_resource_kind_check CHECK(resource_kind='agent');
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check CHECK(state IN (
    'queued','running','cooldown','awaiting_approval','completed',
    'completed_with_errors','failed','canceled','rejected'
));
ALTER TABLE space_runs ADD COLUMN agent_instance_id TEXT REFERENCES space_agent_instances(id) ON DELETE SET NULL;
ALTER TABLE space_runs ADD COLUMN agent_version_id TEXT REFERENCES space_agent_versions(id) ON DELETE SET NULL;
ALTER TABLE space_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1 CHECK(attempt BETWEEN 1 AND 3);
ALTER TABLE space_runs ADD COLUMN next_retry_at TIMESTAMPTZ;

CREATE TABLE space_run_steps (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('queued','running','cooldown','awaiting_approval','completed','completed_with_errors','failed','canceled','rejected')),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK(attempt BETWEEN 1 AND 3),
    input JSONB NOT NULL DEFAULT '{}'::jsonb,
    output JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT,
    error_message TEXT,
    next_retry_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(run_id,node_id)
);

CREATE TABLE space_workflow_event_claims (
    instance_id TEXT NOT NULL REFERENCES space_agent_instances(id) ON DELETE CASCADE,
    workflow_version_id TEXT NOT NULL REFERENCES space_workflow_versions(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    event_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL DEFAULT '',
    run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    state TEXT NOT NULL CHECK(state IN ('claimed','completed','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(instance_id,workflow_version_id,provider,event_id)
);

CREATE TABLE space_workflow_resource_leases (
    resource_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE space_workflow_action_journal (
    idempotency_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    risk TEXT NOT NULL CHECK(risk IN ('read','write','destructive')),
    state TEXT NOT NULL CHECK(state IN ('started','completed','failed','unknown')),
    request JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE space_agent_versions ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE space_agent_version_workflows ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_version_workflows FORCE ROW LEVEL SECURITY;
ALTER TABLE space_agent_instances ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_instances FORCE ROW LEVEL SECURITY;
ALTER TABLE space_agent_instance_workflows ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_instance_workflows FORCE ROW LEVEL SECURITY;
ALTER TABLE space_agent_memory_events ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_memory_events FORCE ROW LEVEL SECURITY;
ALTER TABLE space_run_steps ENABLE ROW LEVEL SECURITY; ALTER TABLE space_run_steps FORCE ROW LEVEL SECURITY;
ALTER TABLE space_workflow_event_claims ENABLE ROW LEVEL SECURITY; ALTER TABLE space_workflow_event_claims FORCE ROW LEVEL SECURITY;
ALTER TABLE space_workflow_resource_leases ENABLE ROW LEVEL SECURITY; ALTER TABLE space_workflow_resource_leases FORCE ROW LEVEL SECURITY;
ALTER TABLE space_workflow_action_journal ENABLE ROW LEVEL SECURITY; ALTER TABLE space_workflow_action_journal FORCE ROW LEVEL SECURITY;

CREATE POLICY space_agent_versions_member_read ON space_agent_versions FOR SELECT USING(misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_agent_versions_creator_write ON space_agent_versions FOR ALL USING(misty_rls_is_service() OR creator_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR creator_user_id=misty_rls_user_id());
CREATE POLICY space_agent_version_workflows_member_read ON space_agent_version_workflows FOR SELECT USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_versions v WHERE v.id=agent_version_id AND misty_is_space_member(v.space_id)));
CREATE POLICY space_agent_version_workflows_creator_write ON space_agent_version_workflows FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_versions v WHERE v.id=agent_version_id AND v.creator_user_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_versions v WHERE v.id=agent_version_id AND v.creator_user_id=misty_rls_user_id()));
CREATE POLICY space_agent_instances_private ON space_agent_instances FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY space_agent_instance_workflows_private ON space_agent_instance_workflows FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_instances i WHERE i.id=instance_id AND i.user_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_instances i WHERE i.id=instance_id AND i.user_id=misty_rls_user_id()));
CREATE POLICY space_agent_memory_private ON space_agent_memory_events FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_instances i WHERE i.id=instance_id AND i.user_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_instances i WHERE i.id=instance_id AND i.user_id=misty_rls_user_id()));
CREATE POLICY space_run_steps_private ON space_run_steps FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id AND r.requesting_member_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id AND r.requesting_member_id=misty_rls_user_id()));
CREATE POLICY space_workflow_event_claims_private ON space_workflow_event_claims FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_instances i WHERE i.id=instance_id AND i.user_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_agent_instances i WHERE i.id=instance_id AND i.user_id=misty_rls_user_id()));
CREATE POLICY space_workflow_resource_leases_private ON space_workflow_resource_leases FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id AND r.requesting_member_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id AND r.requesting_member_id=misty_rls_user_id()));
CREATE POLICY space_workflow_action_journal_private ON space_workflow_action_journal FOR ALL USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id AND r.requesting_member_id=misty_rls_user_id())) WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id AND r.requesting_member_id=misty_rls_user_id()));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_agent_versions,space_agent_version_workflows,space_agent_instances,space_agent_instance_workflows,space_agent_memory_events,space_run_steps,space_workflow_event_claims,space_workflow_resource_leases,space_workflow_action_journal TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE space_agent_memory_events_id_seq TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- Down removes only v2 execution structures. Retired legacy tables and
-- standalone workflow runs are intentionally not reconstructed.
-- +goose Down
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';
DROP TABLE IF EXISTS space_workflow_action_journal,space_workflow_resource_leases,space_workflow_event_claims,space_run_steps,space_agent_memory_events,space_agent_instance_workflows,space_agent_instances,space_agent_version_workflows CASCADE;
ALTER TABLE space_agents DROP CONSTRAINT IF EXISTS space_agents_published_version_fkey;
DROP TABLE IF EXISTS space_agent_versions CASCADE;
ALTER TABLE space_runs DROP COLUMN IF EXISTS next_retry_at,DROP COLUMN IF EXISTS attempt,DROP COLUMN IF EXISTS agent_version_id,DROP COLUMN IF EXISTS agent_instance_id;
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check CHECK(state IN ('queued','running','awaiting_approval','completed','failed','canceled','retrying'));
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_resource_kind_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_resource_kind_check CHECK(resource_kind IN ('agent','workflow'));
ALTER TABLE space_agents DROP COLUMN IF EXISTS published_agent_version_id,DROP COLUMN IF EXISTS access_policy;
-- +goose StatementEnd
