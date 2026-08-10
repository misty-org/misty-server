-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Space conversations have one participant model. Authenticated people and
-- installed Agents occupy the same table, while only people are login
-- principals. Existing person rows are preserved in place.
ALTER TABLE space_conversation_members DROP CONSTRAINT space_conversation_members_pkey;
ALTER TABLE space_conversation_members ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE space_conversation_members
    ADD COLUMN agent_id TEXT REFERENCES personal_agents(id) ON DELETE CASCADE,
    ADD COLUMN actor_kind TEXT NOT NULL DEFAULT 'person'
        CHECK(actor_kind IN ('person','agent')),
    ADD CONSTRAINT space_conversation_member_actor_check CHECK(
        (actor_kind='person' AND user_id IS NOT NULL AND agent_id IS NULL) OR
        (actor_kind='agent' AND user_id IS NULL AND agent_id IS NOT NULL)
    );
CREATE UNIQUE INDEX space_conversation_members_person_unique
    ON space_conversation_members(conversation_id,user_id) WHERE actor_kind='person';
CREATE UNIQUE INDEX space_conversation_members_agent_unique
    ON space_conversation_members(conversation_id,agent_id) WHERE actor_kind='agent';
CREATE INDEX space_conversation_members_agent_idx
    ON space_conversation_members(agent_id,conversation_id) WHERE actor_kind='agent';

-- Direct conversations are canonical for one person and one Agent in a Space.
DELETE FROM space_messages WHERE conversation_id IN (
    SELECT id FROM space_conversations WHERE kind='misty_support'
);
DELETE FROM space_conversations WHERE kind='misty_support';

-- "Misty" is now only the default personal Space name. Retire the canonical
-- support-space topology and provision an ordinary fully featured Space for
-- every active account.
DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
DROP FUNCTION IF EXISTS misty_provision_default_space_for_new_user();
DROP FUNCTION IF EXISTS misty_ensure_default_space(TEXT);

UPDATE spaces SET kind='standard',updated_at=NOW() WHERE kind='misty';
DELETE FROM space_members sm USING spaces s
WHERE sm.space_id=s.id AND s.id='space_misty_canonical' AND sm.user_id<>s.owner_user_id;
UPDATE space_roles SET permissions=
    '["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage","agents.run"]'::jsonb,
    version=version+1,updated_at=NOW()
WHERE space_id='space_misty_canonical' AND is_everyone;

CREATE OR REPLACE FUNCTION misty_ensure_default_space(candidate_user_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    default_space_id TEXT := 'space_default_' || md5(candidate_user_id);
    default_domain_id TEXT := 'sd_default_' || md5(candidate_user_id);
    existing_space_id TEXT;
BEGIN
    IF candidate_user_id IS NULL OR candidate_user_id='' THEN RETURN NULL; END IF;
    PERFORM pg_advisory_xact_lock(hashtext('default-space:' || candidate_user_id));
    SELECT id INTO existing_space_id FROM spaces
        WHERE owner_user_id=candidate_user_id AND name='Misty' AND lifecycle_state='active'
        ORDER BY created_at LIMIT 1;
    IF existing_space_id IS NOT NULL THEN RETURN existing_space_id; END IF;
    INSERT INTO security_domains(id,kind,owner_user_id,space_id)
        VALUES(default_domain_id,'space',candidate_user_id,default_space_id);
    INSERT INTO spaces(id,owner_user_id,name,security_domain_id,kind)
        VALUES(default_space_id,candidate_user_id,'Misty',default_domain_id,'standard');
    INSERT INTO space_storage_usage(space_id) VALUES(default_space_id);
    INSERT INTO space_members(space_id,user_id,role) VALUES(default_space_id,candidate_user_id,'owner');
    INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
        VALUES('role_default_'||md5(candidate_user_id),default_space_id,'@everyone',TRUE,
        '["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage","agents.run"]'::jsonb);
    RETURN default_space_id;
END
$$;

CREATE OR REPLACE FUNCTION misty_provision_default_space_for_new_user()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp SET row_security = off AS $$
BEGIN
    PERFORM misty_ensure_default_space(NEW.id);
    RETURN NEW;
END
$$;
CREATE TRIGGER users_provision_default_misty_space
AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION misty_provision_default_space_for_new_user();
SELECT misty_ensure_default_space(id) FROM users WHERE lifecycle_state='active';
ALTER TABLE space_conversations
    ADD COLUMN direct_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN direct_agent_id TEXT REFERENCES personal_agents(id) ON DELETE CASCADE;
ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_kind_check;
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_kind_check
    CHECK(kind IN ('standard','direct'));
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_direct_actor_check CHECK(
    (kind='direct' AND direct_user_id IS NOT NULL AND direct_agent_id IS NOT NULL) OR
    (kind<>'direct' AND direct_user_id IS NULL AND direct_agent_id IS NULL)
);
CREATE UNIQUE INDEX space_conversations_direct_unique
    ON space_conversations(space_id,direct_user_id,direct_agent_id)
    WHERE kind='direct' AND direct_agent_id IS NOT NULL;

-- Requested capabilities and readable context are immutable version data.
-- Older rows receive the only snapshot available from their current
-- definition because earlier schemas did not version these fields.
ALTER TABLE personal_agent_versions
    ADD COLUMN context_permissions JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK(jsonb_typeof(context_permissions)='object'),
    ADD COLUMN tool_permissions JSONB NOT NULL DEFAULT '{"grants":[]}'::jsonb
        CHECK(jsonb_typeof(tool_permissions)='object');
UPDATE personal_agent_versions v SET
    context_permissions=a.context_permissions,
    tool_permissions=a.tool_permissions
FROM personal_agents a WHERE a.id=v.agent_id;

-- A Space placement owns its role and approved capability subset. Old
-- per-person audience grants are intentionally widened to the full Space: the
-- normal Space permission system now decides who may invoke an Agent.
ALTER TABLE personal_agent_space_grants
    ADD COLUMN role_id TEXT REFERENCES space_roles(id) ON DELETE RESTRICT,
    ADD COLUMN capability_grants JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK(jsonb_typeof(capability_grants)='array');
INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
SELECT 'role_agent_member_'||md5(s.id),s.id,'Agent member',FALSE,
    '["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","tasks.view","tasks.manage","agents.run"]'::jsonb
FROM spaces s
WHERE NOT EXISTS(SELECT 1 FROM space_roles r WHERE r.space_id=s.id AND r.name='Agent member');
UPDATE personal_agent_space_grants g SET
    all_members=TRUE,
    role_id=(SELECT r.id FROM space_roles r WHERE r.space_id=g.space_id AND r.name='Agent member' LIMIT 1),
    capability_grants=COALESCE((
        SELECT jsonb_agg(DISTINCT grant_item)
        FROM personal_agent_versions v,
             jsonb_array_elements(COALESCE(v.tool_permissions->'grants','[]'::jsonb)) grant_item
        WHERE v.id=g.approved_version_id AND grant_item ? 'capability'
    ),'[]'::jsonb)
WHERE g.removed_at IS NULL;
DELETE FROM personal_agent_member_grants;

-- Device access is granted by the requesting person, for one Agent, Space,
-- device and opaque local scope. Paths and credentials never enter this table.
CREATE TABLE agent_device_grants (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    scope_id TEXT NOT NULL,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(capabilities)='array'),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id,agent_id,space_id,device_id,scope_id)
);
CREATE INDEX agent_device_grants_active_idx
    ON agent_device_grants(user_id,space_id,agent_id,expires_at)
    WHERE revoked_at IS NULL;
ALTER TABLE agent_device_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_device_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_device_grants_owner_policy ON agent_device_grants FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE workflow_device_node_jobs
    ADD COLUMN device_grant_id TEXT REFERENCES agent_device_grants(id) ON DELETE SET NULL,
    ADD COLUMN assigned_device_id TEXT REFERENCES trusted_devices(id) ON DELETE SET NULL;

-- Message triggers are durable and idempotent. Agent runtime sessions remain
-- private implementation details; clients observe this state and normal Space
-- messages through the Space realtime stream.
CREATE TABLE space_agent_message_triggers (
    id TEXT PRIMARY KEY,
    source_message_id TEXT NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    requesting_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    trigger_kind TEXT NOT NULL CHECK(trigger_kind IN ('direct','mention')),
    state TEXT NOT NULL DEFAULT 'queued'
        CHECK(state IN ('queued','working','awaiting_approval','completed','failed','canceled','retrying')),
    run_id TEXT,
    error_code TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_message_id,agent_id,trigger_kind)
);
CREATE INDEX space_agent_message_triggers_stream_idx
    ON space_agent_message_triggers(space_id,conversation_id,created_at);
ALTER TABLE space_agent_message_triggers ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_message_triggers FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_message_triggers_member_policy ON space_agent_message_triggers FOR SELECT
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM space_members sm
        WHERE sm.space_id=space_agent_message_triggers.space_id
          AND sm.user_id=misty_rls_user_id()
    ));

-- The old chat stores are intentionally not migrated. New Agent DMs begin as
-- normal empty Space conversations and follow Space retention from this point.
DELETE FROM agent_conversation_events;
DELETE FROM agent_conversations;
DELETE FROM space_agent_conversation_events;
DELETE FROM space_agent_conversations;
DELETE FROM personal_agent_instances;

-- Preserve shared history without retaining a runnable built-in persona.
UPDATE space_messages
SET sender_kind='system', sender_agent_id=NULL,
    origin=jsonb_set(COALESCE(origin,'{}'::jsonb),'{author_name}','"Former agent"'::jsonb,TRUE)
WHERE sender_kind='agent' AND sender_agent_id='misty';

-- The compatibility window for the old run source is over. Historical rows
-- were already migrated to the neutral agent_console value in 20260916.
UPDATE space_runs SET source_type='agent_console' WHERE source_type='mika';
UPDATE space_runs SET trigger_kind='agent_console' WHERE trigger_kind='mika';
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_source_type_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_source_type_check CHECK(source_type IN (
    'direct','group_mention','agent_console','studio_test','schedule','connector','task'
));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON agent_device_grants,space_agent_message_triggers TO misty_app;
        GRANT EXECUTE ON FUNCTION misty_ensure_default_space(TEXT) TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
DROP FUNCTION IF EXISTS misty_provision_default_space_for_new_user();
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_source_type_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_source_type_check CHECK(source_type IN (
    'direct','group_mention','agent_console','studio_test','schedule','connector','task','mika'
));
DROP TABLE IF EXISTS space_agent_message_triggers;
ALTER TABLE workflow_device_node_jobs DROP COLUMN IF EXISTS assigned_device_id, DROP COLUMN IF EXISTS device_grant_id;
DROP TABLE IF EXISTS agent_device_grants;
DROP INDEX IF EXISTS space_conversations_direct_unique;
ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_direct_actor_check;
ALTER TABLE space_conversations DROP COLUMN IF EXISTS direct_agent_id, DROP COLUMN IF EXISTS direct_user_id;
ALTER TABLE personal_agent_space_grants DROP COLUMN IF EXISTS capability_grants, DROP COLUMN IF EXISTS role_id;
ALTER TABLE personal_agent_versions DROP COLUMN IF EXISTS context_permissions, DROP COLUMN IF EXISTS tool_permissions;
DROP INDEX IF EXISTS space_conversation_members_agent_idx;
DROP INDEX IF EXISTS space_conversation_members_agent_unique;
DROP INDEX IF EXISTS space_conversation_members_person_unique;
ALTER TABLE space_conversation_members DROP CONSTRAINT IF EXISTS space_conversation_member_actor_check;
DELETE FROM space_conversation_members WHERE actor_kind='agent';
ALTER TABLE space_conversation_members DROP COLUMN IF EXISTS actor_kind, DROP COLUMN IF EXISTS agent_id;
ALTER TABLE space_conversation_members ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE space_conversation_members ADD PRIMARY KEY(conversation_id,user_id);
-- Deleted private histories are deliberately not recreated on rollback.
-- +goose StatementEnd
