-- +goose Up
-- One agent model. Every agent has a runner: Misty's own cloud loop, or one of
-- the coding CLIs installed on the person's device. Ask and Auto replace the
-- account-wide "Ask before acting" switch: Ask pauses before every action that
-- changes something, Auto continues when the person remembered an approval.
-- Remembered approvals only skip a pause; they never limit what an agent tries.

ALTER TABLE misty_ask_identities ADD COLUMN runner text NOT NULL DEFAULT 'misty'
    CHECK (runner IN ('misty', 'claude', 'codex', 'kimi', 'minimax', 'grok', 'pi', 'omp', 'dsh', 'agy', 'opencode', 'qoder', 'qoder-cn'));

-- Full access is gone: it meant the same as Auto once nothing pauses by default.
UPDATE misty_ask_identities SET default_run_mode = 'auto' WHERE default_run_mode = 'full';
UPDATE misty_ask_identity_versions SET default_run_mode = 'auto' WHERE default_run_mode = 'full';
-- People who asked before every action keep doing so, now through their agents.
UPDATE misty_ask_identities SET default_run_mode = 'ask'
    WHERE owner_user_id IN (SELECT user_id FROM ai_user_settings WHERE app_actions_ask);
ALTER TABLE misty_ask_identities DROP CONSTRAINT personal_agents_default_run_mode_check;
ALTER TABLE misty_ask_identities ADD CONSTRAINT personal_agents_default_run_mode_check CHECK (default_run_mode IN ('ask', 'auto'));
ALTER TABLE misty_ask_identity_versions DROP CONSTRAINT personal_agent_versions_default_run_mode_check;
ALTER TABLE misty_ask_identity_versions ADD CONSTRAINT personal_agent_versions_default_run_mode_check CHECK (default_run_mode IN ('ask', 'auto'));

-- Misty stays one managed agent per person; each connected coding tool gets its
-- own managed agent, hidden from the agent switcher.
DROP INDEX personal_agents_one_managed_misty_per_owner_idx;
CREATE UNIQUE INDEX personal_agents_one_managed_misty_per_owner_idx ON misty_ask_identities(owner_user_id)
    WHERE system_managed AND runner = 'misty' AND deleted_at IS NULL;
CREATE UNIQUE INDEX personal_agents_one_engine_per_owner_idx ON misty_ask_identities(owner_user_id, runner)
    WHERE system_managed AND runner <> 'misty' AND deleted_at IS NULL;

-- A chat can choose Ask or Auto; null follows the agent's default.
ALTER TABLE ai_conversation_modes ADD COLUMN permission_mode text CHECK (permission_mode IN ('ask', 'auto'));
ALTER TABLE ai_conversation_modes ALTER COLUMN mode SET DEFAULT 'act';

-- Remembered approvals. The grantor owns the resource the action touches, so a
-- person who only invokes an agent cannot approve actions on someone else's
-- account, and grants never travel with a shared agent. Scopes are concrete:
-- no wildcards, and payments carry a per-payment cap.
CREATE TABLE agent_grants (
    id text PRIMARY KEY CHECK (id ~ '^grant_[0-9a-f-]{36}$'),
    agent_id text NOT NULL REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
    grantor_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action text NOT NULL CHECK (action IN ('app.action', 'browser.send', 'browser.delete', 'browser.publish', 'payment',
        'space.admin', 'item.delete', 'schedule.remove', 'project.edit', 'project.install', 'git.push')),
    scope_kind text NOT NULL CHECK (scope_kind IN ('connected_account', 'origin', 'merchant', 'space', 'project', 'repo', 'schedule')),
    scope_id text NOT NULL CHECK (scope_id <> '' AND scope_id <> '*' AND length(scope_id) <= 300),
    constraints jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(constraints) = 'object'),
    source_request_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    use_count bigint NOT NULL DEFAULT 0 CHECK (use_count >= 0),
    revoked_at timestamptz,
    CHECK (action <> 'payment' OR (constraints ? 'max_amount_minor' AND constraints ? 'currency'))
);
CREATE UNIQUE INDEX agent_grants_active ON agent_grants(agent_id, grantor_user_id, action, scope_kind, scope_id) WHERE revoked_at IS NULL;
CREATE INDEX agent_grants_grantor ON agent_grants(grantor_user_id, agent_id, created_at DESC) WHERE revoked_at IS NULL;
ALTER TABLE agent_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_grants_grantor ON agent_grants
    USING (misty_rls_is_service() OR grantor_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR grantor_user_id = misty_rls_user_id());
CREATE TRIGGER agent_grants_changes AFTER INSERT OR DELETE OR UPDATE ON agent_grants
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agent_grants', 'grantor_user_id');

-- A pause records what remembering it would grant. The action and scope come
-- from the server when it pauses, never from the person's answer.
ALTER TABLE agent_app_requests
    ADD COLUMN agent_id text NOT NULL DEFAULT '',
    ADD COLUMN grant_action text NOT NULL DEFAULT '',
    ADD COLUMN grant_scope_kind text NOT NULL DEFAULT '',
    ADD COLUMN grant_scope_id text NOT NULL DEFAULT '',
    ADD COLUMN grant_constraints jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN remember_label text NOT NULL DEFAULT '',
    ADD COLUMN rememberable boolean NOT NULL DEFAULT false,
    ADD COLUMN permission_mode text NOT NULL DEFAULT '';

-- The live checklist one run published; delegated runs nest under their parent.
CREATE TABLE agent_checklists (
    invocation_id text PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id text NOT NULL,
    parent_invocation_id text,
    items jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(items) = 'array'),
    version int NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_checklists_conversation ON agent_checklists(owner_user_id, conversation_id, updated_at DESC);
ALTER TABLE agent_checklists ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_checklists FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_checklists_owner ON agent_checklists
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

-- Which coding tools each of a person's devices has, and whether they are
-- signed in. Reported by the device; the CLI's own usage limits are best effort.
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

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_grants, agent_checklists, coding_engines TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS coding_engines;
DROP TABLE IF EXISTS agent_checklists;
DROP TABLE IF EXISTS agent_grants;
ALTER TABLE agent_app_requests DROP COLUMN agent_id, DROP COLUMN grant_action, DROP COLUMN grant_scope_kind, DROP COLUMN grant_scope_id,
    DROP COLUMN grant_constraints, DROP COLUMN remember_label, DROP COLUMN rememberable, DROP COLUMN permission_mode;
ALTER TABLE ai_conversation_modes DROP COLUMN permission_mode;
DROP INDEX IF EXISTS personal_agents_one_engine_per_owner_idx;
DROP INDEX IF EXISTS personal_agents_one_managed_misty_per_owner_idx;
DELETE FROM misty_ask_identities WHERE runner <> 'misty';
CREATE UNIQUE INDEX personal_agents_one_managed_misty_per_owner_idx ON misty_ask_identities(owner_user_id) WHERE system_managed AND deleted_at IS NULL;
ALTER TABLE misty_ask_identity_versions DROP CONSTRAINT personal_agent_versions_default_run_mode_check;
ALTER TABLE misty_ask_identity_versions ADD CONSTRAINT personal_agent_versions_default_run_mode_check CHECK (default_run_mode IN ('ask', 'auto', 'full'));
ALTER TABLE misty_ask_identities DROP CONSTRAINT personal_agents_default_run_mode_check;
ALTER TABLE misty_ask_identities ADD CONSTRAINT personal_agents_default_run_mode_check CHECK (default_run_mode IN ('ask', 'auto', 'full'));
ALTER TABLE misty_ask_identities DROP COLUMN runner;
