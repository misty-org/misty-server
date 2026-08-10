-- +goose Up
-- +goose StatementBegin
-- 'assistant' is being removed as a concept, including the persisted event type
-- written by agent/persistence.go. The column is agent_conversation_events
-- .event_type and it carries its own CHECK constraint (created inline in
-- 20260905000000_restore_mika_session_persistence.sql), so the constraint has
-- to be rewritten rather than just the rows updated.
--
-- 'assistant_message' stays permitted for one release so a transcript written
-- by the old binary after this migration still inserts, and so in-flight
-- sessions replay. The reader accepts both values.
--
-- Order matters: drop the constraint before the backfill or the UPDATE is
-- rejected by the constraint still being enforced.
ALTER TABLE agent_conversation_events
    DROP CONSTRAINT IF EXISTS agent_conversation_events_event_type_check;

UPDATE agent_conversation_events
SET event_type = 'agent_message'
WHERE event_type = 'assistant_message';

ALTER TABLE agent_conversation_events ADD CONSTRAINT agent_conversation_events_event_type_check
    CHECK (event_type IN (
        'user_message',
        'agent_message',
        'tool_call',
        'tool_result',
        'error',
        'assistant_message' -- transitional; drop once all deployments write 'agent_message'
    ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_conversation_events
    DROP CONSTRAINT IF EXISTS agent_conversation_events_event_type_check;

UPDATE agent_conversation_events
SET event_type = 'assistant_message'
WHERE event_type = 'agent_message';

ALTER TABLE agent_conversation_events ADD CONSTRAINT agent_conversation_events_event_type_check
    CHECK (event_type IN ('user_message', 'assistant_message', 'tool_call', 'tool_result', 'error'));
-- +goose StatementEnd
