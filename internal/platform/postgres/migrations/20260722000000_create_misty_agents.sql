-- +goose Up
-- +goose StatementBegin
CREATE TABLE trusted_devices (
    id TEXT PRIMARY KEY CHECK (id ~ '^device_[0-9a-f-]{36}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    public_key TEXT NOT NULL CHECK (char_length(public_key) BETWEEN 32 AND 4096),
    key_algorithm TEXT NOT NULL DEFAULT 'ed25519' CHECK (key_algorithm = 'ed25519'),
    capabilities JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(capabilities) = 'object'),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, public_key)
);

CREATE TABLE agent_definitions (
    id TEXT PRIMARY KEY CHECK (id ~ '^agent_[0-9a-f-]{36}$'),
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE RESTRICT,
    scope_id TEXT NOT NULL CHECK (scope_id ~ '^scope_[A-Za-z0-9_-]{8,128}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    instructions TEXT NOT NULL CHECK (char_length(instructions) BETWEEN 1 AND 10000),
    workflow JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(workflow) = 'object'),
    workflow_revision INTEGER NOT NULL DEFAULT 1 CHECK (workflow_revision > 0),
    trust_policy JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(trust_policy) = 'object'),
    cloud_document_consent BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (owner_user_id, device_id, scope_id, name)
);

CREATE TABLE agent_members (
    agent_id TEXT NOT NULL REFERENCES agent_definitions(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role = 'member'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (agent_id, user_id)
);

CREATE TABLE agent_triggers (
    id TEXT PRIMARY KEY CHECK (id ~ '^trigger_[0-9a-f-]{36}$'),
    agent_id TEXT NOT NULL REFERENCES agent_definitions(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('manual','schedule','file_created','file_changed','local_webhook')),
    config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE agent_jobs (
    id TEXT PRIMARY KEY CHECK (id ~ '^job_[0-9a-f-]{36}$'),
    agent_id TEXT REFERENCES agent_definitions(id) ON DELETE SET NULL,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE RESTRICT,
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('manual','schedule','file_created','file_changed','local_webhook')),
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','leased','running','awaiting_approval','completed','failed','canceled','expired')),
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 200),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    result JSONB CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
    error_code TEXT,
    error_message TEXT,
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    lease_token_hash TEXT,
    lease_expires_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '7 days'),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    canceled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (requester_user_id, idempotency_key)
);

CREATE TABLE agent_job_events (
    id BIGSERIAL PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES agent_jobs(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 64),
    data JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(data) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE agent_approvals (
    id TEXT PRIMARY KEY CHECK (id ~ '^approval_[0-9a-f-]{36}$'),
    job_id TEXT NOT NULL REFERENCES agent_jobs(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action_kind TEXT NOT NULL CHECK (char_length(action_kind) BETWEEN 1 AND 64),
    action_summary TEXT NOT NULL CHECK (char_length(action_summary) BETWEEN 1 AND 1000),
    action JSONB NOT NULL CHECK (jsonb_typeof(action) = 'object'),
    action_digest TEXT NOT NULL CHECK (action_digest ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','approved','rejected','expired')),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE agent_conversations (
    id TEXT PRIMARY KEY CHECK (id ~ '^conversation_[0-9a-f-]{36}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id TEXT REFERENCES agent_definitions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE agent_conversation_events (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('user_message','assistant_message','tool_call','tool_result','error')),
    data JSONB NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX trusted_devices_user_active_idx ON trusted_devices(user_id, last_seen_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX agent_definitions_owner_idx ON agent_definitions(owner_user_id, updated_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX agent_members_user_idx ON agent_members(user_id, agent_id);
CREATE INDEX agent_triggers_agent_idx ON agent_triggers(agent_id, enabled);
CREATE INDEX agent_jobs_device_queue_idx ON agent_jobs(device_id, created_at) WHERE state IN ('queued','leased','running');
CREATE INDEX agent_jobs_owner_idx ON agent_jobs(owner_user_id, created_at DESC);
CREATE INDEX agent_job_events_job_idx ON agent_job_events(job_id, id);
CREATE INDEX agent_approvals_owner_pending_idx ON agent_approvals(owner_user_id, expires_at) WHERE state = 'pending';
CREATE INDEX agent_conversation_events_conversation_idx ON agent_conversation_events(conversation_id, id);

ALTER TABLE trusted_devices ENABLE ROW LEVEL SECURITY; ALTER TABLE trusted_devices FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_definitions ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_definitions FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_members ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_members FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_triggers ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_triggers FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_jobs ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_jobs FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_job_events ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_job_events FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_approvals ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_approvals FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_conversations ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_conversations FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_conversation_events ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_conversation_events FORCE ROW LEVEL SECURITY;

CREATE POLICY trusted_devices_user_policy ON trusted_devices FOR ALL USING (misty_rls_is_service() OR user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY agent_definitions_select_policy ON agent_definitions FOR SELECT USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id() OR EXISTS (SELECT 1 FROM agent_members m WHERE m.agent_id = id AND m.user_id = misty_rls_user_id()));
CREATE POLICY agent_definitions_owner_write_policy ON agent_definitions FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY agent_members_select_policy ON agent_members FOR SELECT USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id() OR user_id = misty_rls_user_id());
CREATE POLICY agent_members_owner_write_policy ON agent_members FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY agent_triggers_policy ON agent_triggers FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY agent_jobs_policy ON agent_jobs FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id() OR requester_user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id() OR requester_user_id = misty_rls_user_id());
CREATE POLICY agent_job_events_policy ON agent_job_events FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY agent_approvals_policy ON agent_approvals FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY agent_conversations_policy ON agent_conversations FOR ALL USING (misty_rls_is_service() OR user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY agent_conversation_events_policy ON agent_conversation_events FOR ALL USING (misty_rls_is_service() OR user_id = misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON trusted_devices, agent_definitions, agent_members, agent_triggers, agent_jobs, agent_job_events, agent_approvals, agent_conversations, agent_conversation_events TO misty_app;
        GRANT USAGE, SELECT ON SEQUENCE agent_job_events_id_seq, agent_conversation_events_id_seq TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_conversation_events, agent_conversations, agent_approvals, agent_job_events, agent_jobs, agent_triggers, agent_members, agent_definitions, trusted_devices CASCADE;
-- +goose StatementEnd
