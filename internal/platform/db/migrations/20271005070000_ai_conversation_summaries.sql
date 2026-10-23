-- +goose Up
-- A Misty conversation's older turns, summarized when they no longer fit the
-- model's context next to the recent turns. Later turns load this summary plus
-- every turn after through_invocation_id verbatim. The summary is extended, not
-- rebuilt, as more turns age out.
CREATE TABLE ai_conversation_summaries (
    conversation_id text PRIMARY KEY REFERENCES misty_ask_conversations(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    through_invocation_id text NOT NULL CHECK (char_length(through_invocation_id) BETWEEN 1 AND 200),
    through_created_at timestamptz NOT NULL,
    summarized_turns integer NOT NULL CHECK (summarized_turns > 0),
    summary text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 40000),
    model text NOT NULL DEFAULT '' CHECK (char_length(model) <= 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_conversation_summaries_user ON ai_conversation_summaries(user_id);

ALTER TABLE ai_conversation_summaries ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_conversation_summaries FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_conversation_summaries_owner ON ai_conversation_summaries
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON ai_conversation_summaries TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS ai_conversation_summaries;
