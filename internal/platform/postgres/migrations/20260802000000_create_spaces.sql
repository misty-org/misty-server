-- +goose Up
-- +goose StatementBegin
CREATE TABLE spaces (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX spaces_one_owned_per_user_idx ON spaces(owner_user_id);

CREATE TABLE space_members (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    read_message_seq BIGINT NOT NULL DEFAULT 0,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (space_id, user_id)
);

CREATE UNIQUE INDEX space_members_one_owner_idx ON space_members(space_id) WHERE role='owner';
CREATE INDEX space_members_user_idx ON space_members(user_id);

CREATE TABLE space_invitations (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    invited_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id, invited_user_id)
);

CREATE INDEX space_invitations_user_idx ON space_invitations(invited_user_id, expires_at);

CREATE TABLE space_messages (
    seq BIGSERIAL UNIQUE NOT NULL,
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    sender_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_kind TEXT NOT NULL DEFAULT 'person' CHECK (sender_kind IN ('person', 'agent', 'system')),
    sender_agent_id TEXT,
    content JSONB NOT NULL DEFAULT '[]'::jsonb,
    file_node_ids TEXT[] NOT NULL DEFAULT '{}',
    edited_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days'
);

CREATE INDEX space_messages_history_idx ON space_messages(space_id, seq DESC);
CREATE INDEX space_messages_expiry_idx ON space_messages(expires_at);

CREATE TABLE space_nodes (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    parent_id TEXT REFERENCES space_nodes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('folder', 'link')),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    uploader_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_ciphertext BYTEA,
    target_nonce BYTEA,
    target_key_version SMALLINT,
    mime_type TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT CHECK (size_bytes IS NULL OR size_bytes >= 0),
    stale BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((kind='folder' AND target_ciphertext IS NULL AND target_nonce IS NULL) OR
           (kind='link' AND target_ciphertext IS NOT NULL AND target_nonce IS NOT NULL))
);

CREATE INDEX space_nodes_parent_idx ON space_nodes(space_id, parent_id, display_name);

CREATE TABLE space_agents (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    creator_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    instructions TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1,
    schedules_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_agents_space_idx ON space_agents(space_id, updated_at DESC);

CREATE TABLE space_workflows (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    creator_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    definition JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 1,
    schedules_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_workflows_space_idx ON space_workflows(space_id, updated_at DESC);

CREATE TABLE space_runs (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('agent', 'workflow')),
    resource_id TEXT NOT NULL,
    initiated_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    billing_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    trigger_kind TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'completed', 'failed', 'canceled')),
    input JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX space_runs_space_idx ON space_runs(space_id, created_at DESC);

CREATE TABLE space_events (
    id BIGSERIAL PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    entity_id TEXT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_events_replay_idx ON space_events(id, created_at);
CREATE INDEX space_events_space_idx ON space_events(space_id, id);

CREATE TABLE space_inbox_items (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('unread', 'mention', 'agent', 'approval', 'workflow')),
    message_id TEXT REFERENCES space_messages(id) ON DELETE CASCADE,
    event_id BIGINT REFERENCES space_events(id) ON DELETE CASCADE,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_inbox_user_idx ON space_inbox_items(user_id, kind, id DESC);

CREATE TABLE realtime_tickets (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    after_cursor BIGINT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX realtime_tickets_expiry_idx ON realtime_tickets(expires_at);

CREATE TABLE space_resolve_tickets (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES space_nodes(id) ON DELETE CASCADE,
    disposition TEXT NOT NULL CHECK (disposition IN ('open', 'download')),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_resolve_tickets_expiry_idx ON space_resolve_tickets(expires_at);

CREATE OR REPLACE FUNCTION misty_is_space_member(candidate_space_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT EXISTS (
        SELECT 1 FROM space_members
        WHERE space_id=candidate_space_id AND user_id=misty_rls_user_id()
    )
$$;

CREATE OR REPLACE FUNCTION misty_is_space_owner(candidate_space_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT EXISTS (
        SELECT 1 FROM spaces
        WHERE id=candidate_space_id AND owner_user_id=misty_rls_user_id()
    )
$$;

ALTER TABLE spaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE spaces FORCE ROW LEVEL SECURITY;
CREATE POLICY spaces_read ON spaces FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(id));
CREATE POLICY spaces_owner_write ON spaces FOR ALL USING (misty_rls_is_service() OR misty_is_space_owner(id)) WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

ALTER TABLE space_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_members FORCE ROW LEVEL SECURITY;
CREATE POLICY space_members_read ON space_members FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_members_owner_write ON space_members FOR ALL USING (misty_rls_is_service() OR misty_is_space_owner(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

ALTER TABLE space_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_invites_read ON space_invitations FOR SELECT USING (misty_rls_is_service() OR invited_user_id=misty_rls_user_id() OR misty_is_space_owner(space_id));
CREATE POLICY space_invites_owner_write ON space_invitations FOR ALL USING (misty_rls_is_service() OR invited_user_id=misty_rls_user_id() OR misty_is_space_owner(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

ALTER TABLE space_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY space_messages_member_policy ON space_messages FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_nodes FORCE ROW LEVEL SECURITY;
CREATE POLICY space_nodes_member_policy ON space_nodes FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agents FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agents_member_policy ON space_agents FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY space_workflows_member_policy ON space_workflows FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY space_runs_member_policy ON space_runs FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_events FORCE ROW LEVEL SECURITY;
CREATE POLICY space_events_read ON space_events FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_events_write ON space_events FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_inbox_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_inbox_items FORCE ROW LEVEL SECURITY;
CREATE POLICY space_inbox_user_policy ON space_inbox_items FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE realtime_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE realtime_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY realtime_tickets_user_policy ON realtime_tickets FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE space_resolve_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_resolve_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY space_resolve_tickets_user_policy ON space_resolve_tickets FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_resolve_tickets;
DROP TABLE IF EXISTS realtime_tickets;
DROP TABLE IF EXISTS space_inbox_items;
DROP TABLE IF EXISTS space_events;
DROP TABLE IF EXISTS space_runs;
DROP TABLE IF EXISTS space_workflows;
DROP TABLE IF EXISTS space_agents;
DROP TABLE IF EXISTS space_nodes;
DROP TABLE IF EXISTS space_messages;
DROP TABLE IF EXISTS space_invitations;
DROP TABLE IF EXISTS space_members;
DROP TABLE IF EXISTS spaces;
DROP FUNCTION IF EXISTS misty_is_space_owner(TEXT);
DROP FUNCTION IF EXISTS misty_is_space_member(TEXT);
-- +goose StatementEnd
