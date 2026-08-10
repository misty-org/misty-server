-- +goose Up
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';

-- Space agents are the canonical shared catalog. Device-backed definitions
-- remain execution bindings, but no longer form a second ownership model.
ALTER TABLE space_agents ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE space_agents ADD COLUMN icon TEXT NOT NULL DEFAULT 'bot';
ALTER TABLE space_agents ADD COLUMN status TEXT NOT NULL DEFAULT 'available'
    CHECK (status IN ('draft','available','unavailable','disabled'));
ALTER TABLE space_agents ADD COLUMN runtime_kind TEXT NOT NULL DEFAULT 'cloud'
    CHECK (runtime_kind IN ('cloud','device'));
ALTER TABLE space_agents ADD COLUMN updated_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE agent_definitions ADD COLUMN space_agent_id TEXT REFERENCES space_agents(id) ON DELETE CASCADE;

INSERT INTO space_agents(
    id,space_id,creator_user_id,name,instructions,enabled,version,
    description,icon,status,runtime_kind,updated_by_user_id,created_at,updated_at
)
SELECT
    a.id,a.space_id,a.owner_user_id,a.name,a.instructions,a.enabled,a.version,
    '', 'folder-cog', CASE WHEN a.deleted_at IS NOT NULL THEN 'disabled' WHEN a.enabled THEN 'available' ELSE 'draft' END,
    'device',a.owner_user_id,a.created_at,a.updated_at
FROM agent_definitions a
ON CONFLICT(id) DO UPDATE SET
    space_id=EXCLUDED.space_id,
    runtime_kind='device',
    updated_by_user_id=EXCLUDED.updated_by_user_id;

UPDATE agent_definitions SET space_agent_id=id WHERE space_agent_id IS NULL;
ALTER TABLE agent_definitions ALTER COLUMN space_agent_id SET NOT NULL;
ALTER TABLE agent_definitions ADD CONSTRAINT agent_definitions_one_binding_per_space_agent UNIQUE(space_agent_id);

-- Existing Space workflow records become installable/customizable packages.
ALTER TABLE space_workflows ADD COLUMN stable_identifier TEXT;
ALTER TABLE space_workflows ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE space_workflows ADD COLUMN author_name TEXT NOT NULL DEFAULT '';
ALTER TABLE space_workflows ADD COLUMN tags JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(tags)='array');
ALTER TABLE space_workflows ADD COLUMN suggested_agent_preset JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(suggested_agent_preset)='object');
ALTER TABLE space_workflows ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'custom'
    CHECK (source_kind IN ('custom','installed','forked','legacy'));
ALTER TABLE space_workflows ADD COLUMN forked_from_identifier TEXT;
UPDATE space_workflows SET stable_identifier='space.' || space_id || '.' || id WHERE stable_identifier IS NULL;
ALTER TABLE space_workflows ALTER COLUMN stable_identifier SET NOT NULL;
CREATE UNIQUE INDEX space_workflows_identifier_idx ON space_workflows(space_id,stable_identifier);

CREATE TABLE space_workflow_versions (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES space_workflows(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    stable_identifier TEXT NOT NULL,
    version TEXT NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '',
    author_name TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL CHECK (jsonb_typeof(metadata)='object'),
    definition JSONB NOT NULL CHECK (jsonb_typeof(definition)='object'),
    checksum_sha256 TEXT NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(workflow_id,version),
    UNIQUE(workflow_id,checksum_sha256)
);
CREATE INDEX space_workflow_versions_space_idx ON space_workflow_versions(space_id,created_at DESC);

-- Connections are Space context. Only opaque credential references are stored;
-- credentials themselves remain in the provider vault.
CREATE TABLE space_integrations (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    display_name TEXT NOT NULL,
    credential_reference TEXT NOT NULL,
    granted_permissions JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(granted_permissions)='array'),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','needs_attention','disabled')),
    connected_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,provider,display_name)
);
CREATE INDEX space_integrations_space_idx ON space_integrations(space_id,provider);

-- Every pre-versioned workflow gets an immutable v1 snapshot. Metadata is
-- deliberately explicit even for legacy content so routing never depends on
-- display names alone.
INSERT INTO space_workflow_versions(
    id,workflow_id,space_id,stable_identifier,version,name,description,author_name,
    metadata,definition,checksum_sha256,created_by_user_id,created_at
)
SELECT
    'wfver_' || md5(w.id || ':1'),w.id,w.space_id,w.stable_identifier,'1.0.0',w.name,
    w.description,w.author_name,
    jsonb_build_object(
        'capabilities',jsonb_build_array(jsonb_build_object(
            'id','default','name',w.name,'description',COALESCE(NULLIF(w.description,''),'Run this workflow'),
            'inputs',jsonb_build_array(jsonb_build_object('name','prompt','type','string','required',false)),
            'outputs',jsonb_build_array(jsonb_build_object('name','result','type','object')),
            'readOnly',false,'destructive',false,'confirmationRequired',false,'tags',w.tags
        )),
        'requiredIntegrations','[]'::jsonb,'requiredPermissions','[]'::jsonb,
        'runtime',jsonb_build_object('kind','misty-cloud','compatibility','1'),
        'tags',w.tags
    ),
    w.definition,
    md5(w.definition::text) || md5(w.id || ':1'),
    w.creator_user_id,w.created_at
FROM space_workflows w
ON CONFLICT DO NOTHING;

-- Agents that predate workflow attachment receive a portable default package.
INSERT INTO space_workflows(
    id,space_id,creator_user_id,name,definition,enabled,version,schedules_enabled,
    stable_identifier,description,author_name,tags,suggested_agent_preset,source_kind,created_at,updated_at
)
SELECT
    'workflow_for_' || md5(a.id),a.space_id,a.creator_user_id,a.name || ' Workflow',
    CASE WHEN a.runtime_kind='device'
        THEN COALESCE((SELECT d.workflow FROM agent_definitions d WHERE d.space_agent_id=a.id),'{}'::jsonb)
        ELSE jsonb_build_object('nodes',jsonb_build_array(jsonb_build_object('id','respond','kind','structured_prompt','config',jsonb_build_object('prompt','{{input}}'))),'edges','[]'::jsonb)
    END,
    TRUE,1,FALSE,'space.' || a.space_id || '.agent.' || a.id,
    COALESCE(NULLIF(a.description,''),'Default workflow for ' || a.name),'Misty','["agent"]'::jsonb,
    jsonb_build_object('name',a.name,'icon',a.icon,'description',a.description,'instructions',a.instructions),
    'legacy',a.created_at,a.updated_at
FROM space_agents a
WHERE NOT EXISTS (
    SELECT 1 FROM space_workflows w WHERE w.stable_identifier='space.' || a.space_id || '.agent.' || a.id
)
ON CONFLICT DO NOTHING;

INSERT INTO space_workflow_versions(
    id,workflow_id,space_id,stable_identifier,version,name,description,author_name,
    metadata,definition,checksum_sha256,created_by_user_id,created_at
)
SELECT
    'wfver_' || md5(w.id || ':1'),w.id,w.space_id,w.stable_identifier,'1.0.0',w.name,
    w.description,w.author_name,
    jsonb_build_object(
        'capabilities',jsonb_build_array(jsonb_build_object(
            'id',CASE WHEN a.runtime_kind='device' THEN 'folder-operations' ELSE 'respond' END,
            'name',CASE WHEN a.runtime_kind='device' THEN 'Folder operations' ELSE 'Respond' END,
            'description',COALESCE(NULLIF(a.description,''),a.instructions),
            'inputs',jsonb_build_array(jsonb_build_object('name','prompt','type','string','required',true)),
            'outputs',jsonb_build_array(jsonb_build_object('name','result','type','object')),
            'readOnly',false,
            'destructive',a.runtime_kind='device',
            'confirmationRequired',a.runtime_kind='device',
            'tags',CASE WHEN a.runtime_kind='device' THEN '["files","folders"]'::jsonb ELSE '["assistant"]'::jsonb END
        )),
        'requiredIntegrations','[]'::jsonb,
        'requiredPermissions',CASE WHEN a.runtime_kind='device' THEN '["files.read","files.write"]'::jsonb ELSE '[]'::jsonb END,
        'runtime',jsonb_build_object('kind',CASE WHEN a.runtime_kind='device' THEN 'misty-device' ELSE 'misty-cloud' END,'compatibility','1'),
        'tags',w.tags
    ),
    w.definition,md5(w.definition::text) || md5(w.id || ':1'),w.creator_user_id,w.created_at
FROM space_workflows w
JOIN space_agents a ON w.stable_identifier='space.' || a.space_id || '.agent.' || a.id
ON CONFLICT DO NOTHING;

ALTER TABLE space_agents ADD COLUMN active_workflow_version_id TEXT REFERENCES space_workflow_versions(id) ON DELETE RESTRICT;
UPDATE space_agents a SET active_workflow_version_id=v.id
FROM space_workflows w JOIN space_workflow_versions v ON v.workflow_id=w.id
WHERE w.stable_identifier='space.' || a.space_id || '.agent.' || a.id
  AND a.active_workflow_version_id IS NULL;
ALTER TABLE space_agents ALTER COLUMN active_workflow_version_id SET NOT NULL;

-- A single isolated run contract backs direct chat, shared mentions, Studio
-- tests, schedules, and Mika delegations. Historical rows are retained.
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check CHECK (
    state IN ('queued','running','awaiting_approval','completed','failed','canceled','retrying')
);
ALTER TABLE space_runs ADD COLUMN requesting_member_id TEXT REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE space_runs ADD COLUMN source_conversation_id TEXT;
ALTER TABLE space_runs ADD COLUMN source_type TEXT NOT NULL DEFAULT 'direct'
    CHECK (source_type IN ('direct','group_mention','mika','studio_test','schedule'));
ALTER TABLE space_runs ADD COLUMN agent_id TEXT;
ALTER TABLE space_runs ADD COLUMN workflow_identifier TEXT;
ALTER TABLE space_runs ADD COLUMN workflow_version_id TEXT REFERENCES space_workflow_versions(id) ON DELETE RESTRICT;
ALTER TABLE space_runs ADD COLUMN workflow_version TEXT;
ALTER TABLE space_runs ADD COLUMN capability_id TEXT;
ALTER TABLE space_runs ADD COLUMN progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100);
ALTER TABLE space_runs ADD COLUMN outputs JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(outputs)='object');
ALTER TABLE space_runs ADD COLUMN artifacts JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(artifacts)='array');
ALTER TABLE space_runs ADD COLUMN error_message TEXT;
ALTER TABLE space_runs ADD COLUMN retry_of_run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL;
ALTER TABLE space_runs ADD COLUMN canceled_at TIMESTAMPTZ;
ALTER TABLE space_runs ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE space_runs r SET
    requesting_member_id=r.initiated_by_user_id,
    source_type=CASE WHEN r.trigger_kind='mention' THEN 'group_mention' WHEN r.trigger_kind='schedule' THEN 'schedule' ELSE 'direct' END,
    agent_id=CASE WHEN r.resource_kind='agent' THEN r.resource_id ELSE NULL END,
    workflow_version_id=COALESCE(
        (SELECT a.active_workflow_version_id FROM space_agents a WHERE a.id=r.resource_id AND r.resource_kind='agent'),
        (SELECT v.id FROM space_workflow_versions v WHERE v.workflow_id=r.resource_id ORDER BY v.created_at DESC LIMIT 1)
    ),
    capability_id='default',
    progress=CASE WHEN r.state='completed' THEN 100 ELSE 0 END,
    outputs=r.result;
UPDATE space_runs r SET workflow_identifier=v.stable_identifier,workflow_version=v.version
FROM space_workflow_versions v WHERE v.id=r.workflow_version_id;
ALTER TABLE space_runs ALTER COLUMN requesting_member_id SET NOT NULL;
CREATE INDEX space_runs_agent_idx ON space_runs(agent_id,created_at DESC);
CREATE INDEX space_runs_requester_idx ON space_runs(requesting_member_id,created_at DESC);

CREATE TABLE space_run_actions (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    action_kind TEXT NOT NULL,
    summary TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object'),
    destructive BOOLEAN NOT NULL DEFAULT FALSE,
    state TEXT NOT NULL DEFAULT 'proposed' CHECK (state IN ('proposed','approved','completed','failed','canceled')),
    performed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_run_actions_run_idx ON space_run_actions(run_id,created_at);

CREATE TABLE space_run_approvals (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    requested_from_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    decided_by_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
    action_summary TEXT NOT NULL,
    proposed_actions JSONB NOT NULL CHECK (jsonb_typeof(proposed_actions)='array'),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','approved','rejected','expired','canceled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '24 hours'
);
CREATE INDEX space_run_approvals_run_idx ON space_run_approvals(run_id,created_at);

CREATE TABLE space_agent_conversations (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES space_agents(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX space_agent_conversations_owner_idx ON space_agent_conversations(owner_user_id,updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE space_agent_conversation_events (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES space_agent_conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('user_message','agent_message','run','error')),
    data JSONB NOT NULL CHECK (jsonb_typeof(data)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_agent_conversation_events_idx ON space_agent_conversation_events(conversation_id,id);

ALTER TABLE space_workflow_versions ENABLE ROW LEVEL SECURITY; ALTER TABLE space_workflow_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY space_workflow_versions_policy ON space_workflow_versions FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_integrations ENABLE ROW LEVEL SECURITY; ALTER TABLE space_integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_integrations_policy ON space_integrations FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
DROP POLICY IF EXISTS space_runs_member_policy ON space_runs;
CREATE POLICY space_runs_private_or_shared_policy ON space_runs FOR ALL
    USING (
        misty_rls_is_service() OR
        (source_type='group_mention' AND misty_is_space_member(space_id)) OR
        requesting_member_id=misty_rls_user_id()
    )
    WITH CHECK (
        misty_rls_is_service() OR
        (source_type='group_mention' AND misty_is_space_member(space_id)) OR
        requesting_member_id=misty_rls_user_id()
    );
ALTER TABLE space_run_actions ENABLE ROW LEVEL SECURITY; ALTER TABLE space_run_actions FORCE ROW LEVEL SECURITY;
CREATE POLICY space_run_actions_policy ON space_run_actions FOR ALL
    USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id))
    WITH CHECK (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_runs r WHERE r.id=run_id));
ALTER TABLE space_run_approvals ENABLE ROW LEVEL SECURITY; ALTER TABLE space_run_approvals FORCE ROW LEVEL SECURITY;
CREATE POLICY space_run_approvals_policy ON space_run_approvals FOR ALL
    USING (misty_rls_is_service() OR requested_from_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR requested_from_user_id=misty_rls_user_id());
ALTER TABLE space_agent_conversations ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_conversations_private_policy ON space_agent_conversations FOR ALL
    USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
ALTER TABLE space_agent_conversation_events ENABLE ROW LEVEL SECURITY; ALTER TABLE space_agent_conversation_events FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_conversation_events_private_policy ON space_agent_conversation_events FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_workflow_versions,space_integrations,space_run_actions,space_run_approvals,space_agent_conversations,space_agent_conversation_events TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE space_agent_conversation_events_id_seq TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL app.rls_mode = 'service';
DROP TABLE IF EXISTS space_agent_conversation_events,space_agent_conversations,space_run_approvals,space_run_actions,space_integrations CASCADE;
DROP INDEX IF EXISTS space_runs_requester_idx;
DROP INDEX IF EXISTS space_runs_agent_idx;
DROP POLICY IF EXISTS space_runs_private_or_shared_policy ON space_runs;
DROP POLICY IF EXISTS space_runs_member_policy ON space_runs;
CREATE POLICY space_runs_member_policy ON space_runs FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_runs DROP COLUMN IF EXISTS updated_at,DROP COLUMN IF EXISTS canceled_at,DROP COLUMN IF EXISTS retry_of_run_id,DROP COLUMN IF EXISTS error_message,DROP COLUMN IF EXISTS artifacts,DROP COLUMN IF EXISTS outputs,DROP COLUMN IF EXISTS progress,DROP COLUMN IF EXISTS capability_id,DROP COLUMN IF EXISTS workflow_version,DROP COLUMN IF EXISTS workflow_version_id,DROP COLUMN IF EXISTS workflow_identifier,DROP COLUMN IF EXISTS agent_id,DROP COLUMN IF EXISTS source_type,DROP COLUMN IF EXISTS source_conversation_id,DROP COLUMN IF EXISTS requesting_member_id;
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check CHECK (state IN ('queued','running','completed','failed','canceled'));
ALTER TABLE space_agents DROP COLUMN IF EXISTS active_workflow_version_id;
DROP TABLE IF EXISTS space_workflow_versions CASCADE;
DROP INDEX IF EXISTS space_workflows_identifier_idx;
ALTER TABLE space_workflows DROP COLUMN IF EXISTS forked_from_identifier,DROP COLUMN IF EXISTS source_kind,DROP COLUMN IF EXISTS suggested_agent_preset,DROP COLUMN IF EXISTS tags,DROP COLUMN IF EXISTS author_name,DROP COLUMN IF EXISTS description,DROP COLUMN IF EXISTS stable_identifier;
ALTER TABLE agent_definitions DROP CONSTRAINT IF EXISTS agent_definitions_one_binding_per_space_agent;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS space_agent_id;
DELETE FROM space_agents WHERE runtime_kind='device';
ALTER TABLE space_agents DROP COLUMN IF EXISTS updated_by_user_id,DROP COLUMN IF EXISTS runtime_kind,DROP COLUMN IF EXISTS status,DROP COLUMN IF EXISTS icon,DROP COLUMN IF EXISTS description;
-- +goose StatementEnd
