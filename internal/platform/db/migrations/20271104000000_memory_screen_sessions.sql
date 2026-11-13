-- +goose Up
-- Screen memory: text read from the screen during a capture session the
-- person started. A session groups its moments so the memory tree can show
-- it, and so it can be forgotten whole.
ALTER TABLE memory_events DROP CONSTRAINT memory_events_kind_check;
ALTER TABLE memory_events ADD CONSTRAINT memory_events_kind_check
    CHECK (kind IN ('page', 'voice', 'recording', 'note', 'screen'));
ALTER TABLE memory_events
    ADD COLUMN app text NOT NULL DEFAULT '' CHECK (char_length(app) <= 200),
    ADD COLUMN window_title text NOT NULL DEFAULT '' CHECK (char_length(window_title) <= 300),
    ADD COLUMN session_id text NOT NULL DEFAULT '' CHECK (char_length(session_id) <= 100);
CREATE INDEX memory_events_owner_session ON memory_events (owner_user_id, space_id, session_id)
    WHERE session_id <> '';

-- +goose Down
DROP INDEX memory_events_owner_session;
DELETE FROM memory_events WHERE kind = 'screen';
ALTER TABLE memory_events DROP COLUMN session_id, DROP COLUMN window_title, DROP COLUMN app;
ALTER TABLE memory_events DROP CONSTRAINT memory_events_kind_check;
ALTER TABLE memory_events ADD CONSTRAINT memory_events_kind_check
    CHECK (kind IN ('page', 'voice', 'recording', 'note'));
