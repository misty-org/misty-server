-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- Trusted, short-term working focus for one user's Misty conversation. Rows
-- are written only from successful tool results or explicit UI context, never
-- from unverified assistant prose.
CREATE TABLE misty_conversation_focus (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL CHECK(char_length(btrim(conversation_id)) BETWEEN 1 AND 255),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    entity_kind TEXT NOT NULL CHECK(entity_kind IN ('task','person','note','drawing','calendar_event','roadmap','library_item','message')),
    entity_id TEXT NOT NULL CHECK(char_length(btrim(entity_id)) BETWEEN 1 AND 255),
    label TEXT NOT NULL DEFAULT '' CHECK(char_length(label)<=500),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metadata)='object'),
    source_tool TEXT NOT NULL DEFAULT '' CHECK(char_length(source_tool)<=120),
    source_run_id TEXT NOT NULL DEFAULT '' CHECK(char_length(source_run_id)<=255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,conversation_id,space_id,entity_kind)
);
CREATE INDEX misty_conversation_focus_lookup_idx
    ON misty_conversation_focus(user_id,conversation_id,space_id,updated_at DESC);

CREATE TABLE misty_conversation_pending_actions (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL CHECK(char_length(btrim(conversation_id)) BETWEEN 1 AND 255),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    intent TEXT NOT NULL CHECK(char_length(btrim(intent)) BETWEEN 1 AND 120),
    target_kind TEXT NOT NULL DEFAULT '' CHECK(char_length(target_kind)<=80),
    target_id TEXT NOT NULL DEFAULT '' CHECK(char_length(target_id)<=255),
    target_label TEXT NOT NULL DEFAULT '' CHECK(char_length(target_label)<=500),
    question TEXT NOT NULL CHECK(char_length(btrim(question)) BETWEEN 1 AND 1000),
    original_prompt TEXT NOT NULL DEFAULT '' CHECK(char_length(original_prompt)<=20000),
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(evidence)='array'),
    candidate_intents JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(candidate_intents)='array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,conversation_id,space_id)
);
CREATE INDEX misty_conversation_pending_actions_lookup_idx
    ON misty_conversation_pending_actions(user_id,conversation_id,space_id,updated_at DESC);

ALTER TABLE misty_conversation_focus ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_conversation_focus FORCE ROW LEVEL SECURITY;
CREATE POLICY misty_conversation_focus_owner_policy ON misty_conversation_focus FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE misty_conversation_pending_actions ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_conversation_pending_actions FORCE ROW LEVEL SECURITY;
CREATE POLICY misty_conversation_pending_actions_owner_policy ON misty_conversation_pending_actions FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON misty_conversation_focus TO misty_app;
        GRANT SELECT,INSERT,UPDATE,DELETE ON misty_conversation_pending_actions TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS misty_conversation_pending_actions;
DROP TABLE IF EXISTS misty_conversation_focus;
-- +goose StatementEnd
