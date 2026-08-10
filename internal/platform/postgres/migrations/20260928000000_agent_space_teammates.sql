-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE personal_agent_versions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK(version > 0),
    name TEXT NOT NULL CHECK(char_length(name) BETWEEN 1 AND 80),
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    model_mode TEXT NOT NULL CHECK(model_mode IN ('automatic','pinned')),
    model_id TEXT NOT NULL DEFAULT '',
    reasoning_effort TEXT NOT NULL DEFAULT '',
    checksum_sha256 TEXT NOT NULL CHECK(checksum_sha256 ~ '^[0-9a-f]{64}$'),
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_id,version)
);

INSERT INTO personal_agent_versions(
    id,agent_id,version,name,description,icon,instructions,model_mode,model_id,
    reasoning_effort,checksum_sha256,created_by_user_id,created_at
)
SELECT
    'personalver_' || md5(a.id || ':' || a.version::text),a.id,a.version,a.name,
    a.description,a.icon,a.instructions,a.model_mode,a.model_id,a.reasoning_effort,
    md5(a.id || ':' || a.version::text || ':' || a.name || ':' || a.instructions) ||
        md5(a.model_id || ':' || a.reasoning_effort || ':' || a.description),
    a.owner_user_id,a.updated_at
FROM personal_agents a
ON CONFLICT(agent_id,version) DO NOTHING;

CREATE INDEX personal_agent_versions_checksum_idx
    ON personal_agent_versions(agent_id,checksum_sha256);

ALTER TABLE personal_agent_space_grants
    ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN approved_version_id TEXT REFERENCES personal_agent_versions(id) ON DELETE RESTRICT,
    ADD COLUMN space_instructions TEXT NOT NULL DEFAULT '',
    ADD COLUMN permissions JSONB NOT NULL DEFAULT '{"messages.read":true,"messages.write":true,"tasks.view":true,"tasks.manage":true,"attached_files.read":true}'::jsonb
        CHECK(jsonb_typeof(permissions)='object'),
    ADD COLUMN managed_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK(version > 0),
    ADD COLUMN removed_at TIMESTAMPTZ;

UPDATE personal_agent_space_grants g
SET approved_version_id=v.id,
    managed_by_user_id=g.created_by_user_id,
    all_members=TRUE
FROM personal_agent_versions v
JOIN personal_agents a ON a.id=v.agent_id AND a.version=v.version
WHERE g.agent_id=a.id AND g.approved_version_id IS NULL;

ALTER TABLE personal_agent_space_grants
    ALTER COLUMN approved_version_id SET NOT NULL;

CREATE INDEX personal_agent_space_memberships_active_idx
    ON personal_agent_space_grants(space_id,agent_id) WHERE removed_at IS NULL AND enabled;

ALTER TABLE space_tasks
    ADD COLUMN assignee_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL;
ALTER TABLE space_tasks DROP CONSTRAINT IF EXISTS space_tasks_created_by_agent_id_fkey;
ALTER TABLE space_tasks
    ADD CONSTRAINT space_tasks_single_assignee_check
    CHECK (assignee_user_id IS NULL OR assignee_agent_id IS NULL);
CREATE INDEX space_tasks_agent_assignee_idx
    ON space_tasks(space_id,assignee_agent_id,archived_at,due_at)
    WHERE assignee_agent_id IS NOT NULL;

CREATE TABLE space_task_activity (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES space_tasks(id) ON DELETE CASCADE,
    actor_kind TEXT NOT NULL CHECK(actor_kind IN ('person','agent','system')),
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    actor_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK(kind IN ('assigned','progress','result','failure','completed','status')),
    message TEXT NOT NULL DEFAULT '' CHECK(char_length(message) <= 12000),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metadata)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (actor_kind='person' AND actor_user_id IS NOT NULL AND actor_agent_id IS NULL) OR
        (actor_kind='agent' AND actor_agent_id IS NOT NULL AND actor_user_id IS NULL) OR
        (actor_kind='system' AND actor_user_id IS NULL AND actor_agent_id IS NULL)
    )
);
CREATE INDEX space_task_activity_task_idx ON space_task_activity(task_id,created_at,id);

ALTER TABLE space_runs
    ADD COLUMN IF NOT EXISTS source_task_id TEXT REFERENCES space_tasks(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS action_envelope JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK(jsonb_typeof(action_envelope)='object');

ALTER TABLE personal_agent_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE space_task_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_task_activity FORCE ROW LEVEL SECURITY;

CREATE POLICY personal_agent_versions_owner_read ON personal_agent_versions FOR SELECT
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id()));
CREATE POLICY personal_agent_versions_space_member_read ON personal_agent_versions FOR SELECT
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agent_space_grants g
        WHERE g.agent_id=agent_id AND g.removed_at IS NULL AND misty_is_space_member(g.space_id)));
CREATE POLICY personal_agent_versions_owner_write ON personal_agent_versions FOR ALL
    USING(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id()))
    WITH CHECK(misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id()));

CREATE POLICY personal_agent_space_grants_member_read ON personal_agent_space_grants FOR SELECT
    USING(misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY personal_agent_space_grants_member_write ON personal_agent_space_grants FOR ALL
    USING(misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK(misty_rls_is_service() OR misty_is_space_member(space_id));

CREATE POLICY space_task_activity_member_read ON space_task_activity FOR SELECT
    USING(misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_task_activity_member_write ON space_task_activity FOR ALL
    USING(misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK(misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON personal_agent_versions,space_task_activity TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_task_activity;
ALTER TABLE space_runs DROP COLUMN IF EXISTS action_envelope, DROP COLUMN IF EXISTS source_task_id;
ALTER TABLE space_tasks DROP CONSTRAINT IF EXISTS space_tasks_single_assignee_check;
ALTER TABLE space_tasks DROP COLUMN IF EXISTS assignee_agent_id;
UPDATE space_tasks t SET created_by_agent_id=NULL
WHERE created_by_agent_id IS NOT NULL AND NOT EXISTS(
    SELECT 1 FROM space_agents a WHERE a.id=t.created_by_agent_id);
ALTER TABLE space_tasks
    ADD CONSTRAINT space_tasks_created_by_agent_id_fkey
    FOREIGN KEY(created_by_agent_id) REFERENCES space_agents(id) ON DELETE SET NULL;
ALTER TABLE personal_agent_space_grants
    DROP COLUMN IF EXISTS removed_at,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS managed_by_user_id,
    DROP COLUMN IF EXISTS permissions,
    DROP COLUMN IF EXISTS space_instructions,
    DROP COLUMN IF EXISTS approved_version_id,
    DROP COLUMN IF EXISTS enabled;
DROP TABLE IF EXISTS personal_agent_versions;
-- +goose StatementEnd
