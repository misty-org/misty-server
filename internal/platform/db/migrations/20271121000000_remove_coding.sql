-- +goose Up
-- Coding is gone: no coding tools, projects or Space code section, and agents
-- run on Misty's own loop only. Folders stay shared read-only, per chat. Old
-- Library zips keep their 'code' artifact kind, since they stay as plain files.
DROP TABLE space_code_projects;
DROP TABLE coding_leases;
DROP TABLE coding_engines;

DELETE FROM agent_grants
    WHERE action IN ('project.edit', 'project.install', 'git.push') OR scope_kind IN ('project', 'repo');
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_action_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_action_check CHECK (action IN ('app.action', 'browser.send', 'browser.delete',
    'browser.publish', 'payment', 'space.admin', 'item.delete', 'schedule.remove', 'recall.read'));
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_scope_kind_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_scope_kind_check CHECK (scope_kind IN ('connected_account', 'origin', 'merchant',
    'space', 'schedule', 'recall'));

DELETE FROM ai_invocation_contexts WHERE kind = 'project_folder';
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace', 'inbox'));

DROP INDEX personal_agents_one_engine_per_owner_idx;
DROP INDEX personal_agents_one_managed_misty_per_owner_idx;
DELETE FROM misty_ask_identities WHERE runner <> 'misty';
CREATE UNIQUE INDEX personal_agents_one_managed_misty_per_owner_idx ON misty_ask_identities(owner_user_id)
    WHERE system_managed AND deleted_at IS NULL;
ALTER TABLE misty_ask_identities DROP COLUMN runner;

-- +goose Down
ALTER TABLE misty_ask_identities ADD COLUMN runner text NOT NULL DEFAULT 'misty'
    CHECK (runner IN ('misty', 'claude', 'codex', 'kimi', 'minimax', 'grok', 'pi', 'omp', 'dsh', 'agy', 'opencode', 'qoder', 'qoder-cn'));
DROP INDEX personal_agents_one_managed_misty_per_owner_idx;
CREATE UNIQUE INDEX personal_agents_one_managed_misty_per_owner_idx ON misty_ask_identities(owner_user_id)
    WHERE system_managed AND runner = 'misty' AND deleted_at IS NULL;
CREATE UNIQUE INDEX personal_agents_one_engine_per_owner_idx ON misty_ask_identities(owner_user_id, runner)
    WHERE system_managed AND runner <> 'misty' AND deleted_at IS NULL;

ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace', 'inbox', 'project_folder'));

ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_action_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_action_check CHECK (action IN ('app.action', 'browser.send', 'browser.delete',
    'browser.publish', 'payment', 'space.admin', 'item.delete', 'schedule.remove', 'project.edit', 'project.install', 'git.push',
    'recall.read'));
ALTER TABLE agent_grants DROP CONSTRAINT agent_grants_scope_kind_check;
ALTER TABLE agent_grants ADD CONSTRAINT agent_grants_scope_kind_check CHECK (scope_kind IN ('connected_account', 'origin', 'merchant',
    'space', 'project', 'repo', 'schedule', 'recall'));

CREATE TABLE coding_engines (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id text NOT NULL,
    engine text NOT NULL CHECK (engine IN ('claude', 'codex', 'kimi', 'minimax', 'grok', 'pi', 'omp', 'dsh', 'agy', 'opencode', 'qoder', 'qoder-cn')),
    installed boolean NOT NULL DEFAULT false,
    signed_in boolean NOT NULL DEFAULT false,
    version text NOT NULL DEFAULT '' CHECK (length(version) <= 80),
    rate_limit jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(rate_limit) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, device_id, engine)
);
ALTER TABLE coding_engines ENABLE ROW LEVEL SECURITY;
ALTER TABLE coding_engines FORCE ROW LEVEL SECURITY;
CREATE POLICY coding_engines_owner ON coding_engines
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE TRIGGER coding_engines_changes AFTER INSERT OR DELETE OR UPDATE ON coding_engines
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('coding_engines', 'user_id');

CREATE TABLE coding_leases (
    id text PRIMARY KEY CHECK (id ~ '^lease_[0-9a-f-]{36}$'),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invocation_id text NOT NULL,
    device_id text NOT NULL,
    engine text NOT NULL,
    model text NOT NULL DEFAULT '' CHECK (length(model) <= 200),
    token_hash text NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    token_budget bigint NOT NULL CHECK (token_budget > 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX coding_leases_user_week ON coding_leases(user_id, created_at DESC);
CREATE INDEX coding_leases_invocation ON coding_leases(invocation_id);
ALTER TABLE coding_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE coding_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY coding_leases_owner ON coding_leases
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

CREATE TABLE space_code_projects (
    id text PRIMARY KEY CHECK (id ~ '^codeproject_[0-9a-f-]{36}$'),
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id text NOT NULL CHECK (char_length(project_id) BETWEEN 1 AND 200),
    device_id text NOT NULL DEFAULT '' CHECK (char_length(device_id) <= 200),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    github_repo text NOT NULL DEFAULT '' CHECK (github_repo = '' OR github_repo ~ '^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$'),
    linked_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    visibility text NOT NULL CHECK (visibility IN ('public', 'private')),
    UNIQUE (space_id, owner_user_id, project_id),
    FOREIGN KEY (space_id, owner_user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX space_code_projects_space ON space_code_projects(space_id, updated_at DESC);
ALTER TABLE space_code_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_code_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY space_code_projects_members ON space_code_projects
    USING (misty_rls_is_service() OR (misty_is_space_member(space_id)
        AND (visibility = 'public' OR owner_user_id = misty_rls_user_id())))
    WITH CHECK (misty_rls_is_service());
CREATE TRIGGER space_code_projects_default_visibility BEFORE INSERT ON space_code_projects FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_code_projects_visibility_guard BEFORE UPDATE OF visibility ON space_code_projects FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('owner_user_id');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON coding_engines, coding_leases, space_code_projects TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
