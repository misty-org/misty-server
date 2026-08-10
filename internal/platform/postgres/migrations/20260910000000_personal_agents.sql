-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE personal_agents (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    model_mode TEXT NOT NULL DEFAULT 'automatic' CHECK (model_mode IN ('automatic','pinned')),
    model_id TEXT NOT NULL DEFAULT '',
    context_permissions JSONB NOT NULL DEFAULT '{"space_chat":true,"library":true,"notes":true,"tasks":true,"members":true}'::jsonb,
    tool_permissions JSONB NOT NULL DEFAULT '{"read":true,"write":false,"integrations":[]}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1,
    source_space_agent_id TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CHECK (model_mode='automatic' OR char_length(model_id)>0)
);

CREATE INDEX personal_agents_owner_recent_idx
    ON personal_agents(owner_user_id,updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE personal_agent_space_grants (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    all_members BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_id,space_id)
);

CREATE TABLE personal_agent_member_grants (
    grant_id TEXT NOT NULL REFERENCES personal_agent_space_grants(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(grant_id,user_id)
);

CREATE TABLE personal_agent_instances (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE CASCADE,
    invoker_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
    scope_key TEXT NOT NULL,
    memory JSONB NOT NULL DEFAULT '[]'::jsonb,
    legacy_memory JSONB NOT NULL DEFAULT '[]'::jsonb,
    legacy_configuration JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_space_agent_instance_id TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_id,invoker_user_id,scope_key)
);

ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS personal_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL;
ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL;
ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS model_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS model_catalog_version TEXT NOT NULL DEFAULT '';

ALTER TABLE library_processing_jobs ADD COLUMN IF NOT EXISTS billing_user_id TEXT REFERENCES users(id) ON DELETE SET NULL;
UPDATE library_processing_jobs j SET billing_user_id=COALESCE(p.enabled_by_user_id,i.added_by_user_id)
FROM space_library_items i LEFT JOIN space_library_intelligence_policies p ON p.space_id=i.space_id
WHERE j.target_kind='space_library_item' AND j.target_id=i.id AND j.billing_user_id IS NULL;

INSERT INTO personal_agents(
    id,owner_user_id,name,description,icon,instructions,enabled,version,source_space_agent_id,created_at,updated_at
)
SELECT 'personal_' || substr(md5(a.id),1,24),a.creator_user_id,a.name,a.description,a.icon,a.instructions,
       a.enabled,a.version,a.id,a.created_at,a.updated_at
FROM space_agents a
ON CONFLICT(source_space_agent_id) DO NOTHING;

INSERT INTO personal_agent_space_grants(id,agent_id,space_id,all_members,created_by_user_id,created_at,updated_at)
SELECT 'agentgrant_' || substr(md5(a.id || ':' || a.space_id),1,24),p.id,a.space_id,
       COALESCE(a.access_policy->>'mode','space')='space',p.owner_user_id,a.created_at,a.updated_at
FROM space_agents a JOIN personal_agents p ON p.source_space_agent_id=a.id
ON CONFLICT(agent_id,space_id) DO NOTHING;

INSERT INTO personal_agent_member_grants(grant_id,user_id)
SELECT g.id,member_id
FROM space_agents a
JOIN personal_agents p ON p.source_space_agent_id=a.id
JOIN personal_agent_space_grants g ON g.agent_id=p.id AND g.space_id=a.space_id
CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(a.access_policy->'allowedUserIds','[]'::jsonb)) AS members(member_id)
JOIN space_members m ON m.space_id=a.space_id AND m.user_id=members.member_id
ON CONFLICT DO NOTHING;

-- Preserve each legacy member instance and its private history. The legacy
-- workflow/runtime rows remain in place for audit compatibility; new chats use
-- the personal instance keyed by the same invoker, Agent, and Space.
INSERT INTO personal_agent_instances(
    id,agent_id,invoker_user_id,space_id,scope_key,legacy_memory,
    legacy_configuration,source_space_agent_instance_id,created_at,updated_at
)
SELECT
    'agentinstance_' || substr(md5(i.id),1,24),p.id,i.user_id,i.space_id,i.space_id,
    COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'kind',e.kind,'data',e.data,'created_at',e.created_at
        ) ORDER BY e.id)
        FROM space_agent_memory_events e WHERE e.instance_id=i.id
    ),'[]'::jsonb),
    jsonb_build_object(
        'connection_bindings',i.connection_bindings,
        'capability_grants',i.capability_grants,
        'legacy_agent_version_id',i.agent_version_id
    ),
    i.id,i.created_at,i.updated_at
FROM space_agent_instances i
JOIN personal_agents p ON p.source_space_agent_id=i.agent_id
ON CONFLICT(source_space_agent_instance_id) DO NOTHING;

UPDATE space_agents SET schedules_enabled=FALSE WHERE schedules_enabled;
UPDATE space_agent_instance_workflows SET enabled=FALSE WHERE enabled;

ALTER TABLE personal_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_agents FORCE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_space_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_space_grants FORCE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_member_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_member_grants FORCE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_instances FORCE ROW LEVEL SECURITY;

CREATE POLICY personal_agents_owner_policy ON personal_agents FOR ALL
    USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY personal_agent_space_grants_owner_policy ON personal_agent_space_grants FOR ALL
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id()))
    WITH CHECK(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id()));
CREATE POLICY personal_agent_member_grants_owner_policy ON personal_agent_member_grants FOR ALL
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agent_space_grants g JOIN personal_agents a ON a.id=g.agent_id
        WHERE g.id=grant_id AND a.owner_user_id=misty_rls_user_id()))
    WITH CHECK(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agent_space_grants g JOIN personal_agents a ON a.id=g.agent_id
        WHERE g.id=grant_id AND a.owner_user_id=misty_rls_user_id()));
CREATE POLICY personal_agent_instances_invoker_policy ON personal_agent_instances FOR ALL
    USING(misty_rls_is_service() OR invoker_user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR invoker_user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON personal_agents,personal_agent_space_grants,
            personal_agent_member_grants,personal_agent_instances TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_conversations DROP COLUMN IF EXISTS model_catalog_version;
ALTER TABLE agent_conversations DROP COLUMN IF EXISTS model_id;
ALTER TABLE agent_conversations DROP COLUMN IF EXISTS space_id;
ALTER TABLE agent_conversations DROP COLUMN IF EXISTS personal_agent_id;
ALTER TABLE library_processing_jobs DROP COLUMN IF EXISTS billing_user_id;
DROP TABLE IF EXISTS personal_agent_instances,personal_agent_member_grants,personal_agent_space_grants,personal_agents;
-- +goose StatementEnd
