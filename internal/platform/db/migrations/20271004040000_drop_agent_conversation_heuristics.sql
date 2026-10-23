-- +goose Up
-- Agents no longer track keyword-derived conversation focus or pending
-- clarifications; the model resolves follow-ups from the conversation itself.
DROP TABLE IF EXISTS misty_conversation_pending_actions;
DROP TABLE IF EXISTS misty_conversation_focus;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'drop_agent_conversation_heuristics is irreversible; restore from a backup instead';
END
$$;
-- +goose StatementEnd
