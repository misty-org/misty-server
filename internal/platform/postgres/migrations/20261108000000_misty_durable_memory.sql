-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE ai_user_settings
    ADD COLUMN memory_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- Misty memories are always private to one user. A Space-scoped memory helps
-- Misty work consistently inside that Space, but it is never shared with the
-- Space's other members and becomes unreadable when membership is lost.
CREATE TABLE misty_memories (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
    scope_key TEXT NOT NULL CHECK(char_length(scope_key) BETWEEN 1 AND 255),
    memory_key TEXT NOT NULL CHECK(char_length(memory_key)=64),
    kind TEXT NOT NULL DEFAULT 'fact' CHECK(kind IN ('fact','preference','instruction')),
    content TEXT NOT NULL CHECK(char_length(btrim(content)) BETWEEN 1 AND 1000),
    reason TEXT NOT NULL DEFAULT '' CHECK(char_length(reason)<=500),
    source_conversation_id TEXT,
    source_invocation_id TEXT REFERENCES ai_invocations(id) ON DELETE SET NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    forgotten_at TIMESTAMPTZ,
    UNIQUE(user_id,scope_key,memory_key)
);
CREATE INDEX misty_memories_active_user_idx
    ON misty_memories(user_id,updated_at DESC)
    WHERE forgotten_at IS NULL;
CREATE INDEX misty_memories_active_space_idx
    ON misty_memories(user_id,space_id,updated_at DESC)
    WHERE forgotten_at IS NULL AND space_id IS NOT NULL;

ALTER TABLE misty_memories ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_memories FORCE ROW LEVEL SECURITY;
CREATE POLICY misty_memories_owner_policy ON misty_memories FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON misty_memories TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS misty_memories;
ALTER TABLE ai_user_settings DROP COLUMN IF EXISTS memory_enabled;
-- +goose StatementEnd
