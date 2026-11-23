-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_agent_threads (
    id text PRIMARY KEY CHECK (char_length(id) BETWEEN 1 AND 128),
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    agent_id text NOT NULL CHECK (char_length(agent_id) BETWEEN 1 AND 200),
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 200),
    state text NOT NULL DEFAULT 'resting' CHECK (state IN ('working', 'needs-you', 'landing', 'resting')),
    continued_from text CHECK (continued_from IS NULL OR char_length(continued_from) BETWEEN 1 AND 128),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (space_id, owner_user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX space_agent_threads_agent ON space_agent_threads(space_id, agent_id, updated_at DESC);

CREATE TABLE space_agent_thread_entries (
    thread_id text NOT NULL REFERENCES space_agent_threads(id) ON DELETE CASCADE,
    key text NOT NULL CHECK (char_length(key) BETWEEN 1 AND 160),
    seq bigint GENERATED ALWAYS AS IDENTITY,
    kind text NOT NULL CHECK (kind IN ('ask', 'reply', 'step', 'output')),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 20000),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (thread_id, key)
);
CREATE INDEX space_agent_thread_entries_order ON space_agent_thread_entries(thread_id, seq);

CREATE TABLE space_agent_thread_reactions (
    thread_id text NOT NULL,
    key text NOT NULL,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji text NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (thread_id, key, user_id, emoji),
    FOREIGN KEY (thread_id, key) REFERENCES space_agent_thread_entries(thread_id, key) ON DELETE CASCADE
);

ALTER TABLE space_agent_threads ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_threads FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_threads_members ON space_agent_threads
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service());

ALTER TABLE space_agent_thread_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_thread_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_thread_entries_members ON space_agent_thread_entries
    USING (misty_rls_is_service() OR EXISTS (SELECT 1 FROM space_agent_threads t
        WHERE t.id = thread_id AND misty_is_space_member(t.space_id)))
    WITH CHECK (misty_rls_is_service());

ALTER TABLE space_agent_thread_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_thread_reactions FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_thread_reactions_members ON space_agent_thread_reactions
    USING (misty_rls_is_service() OR EXISTS (SELECT 1 FROM space_agent_threads t
        WHERE t.id = thread_id AND misty_is_space_member(t.space_id)))
    WITH CHECK (misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_agent_threads TO misty_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_agent_thread_entries TO misty_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_agent_thread_reactions TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS space_agent_thread_reactions;
DROP TABLE IF EXISTS space_agent_thread_entries;
DROP TABLE IF EXISTS space_agent_threads;
