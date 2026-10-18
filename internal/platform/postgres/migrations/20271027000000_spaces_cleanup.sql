-- +goose Up
-- Tasks, roadmaps and Misty's own calendar left index functions and search
-- documents behind when their tables were dropped.
DELETE FROM ai_retrieval_documents WHERE source_kind IN ('task', 'roadmap', 'calendar');
DROP FUNCTION IF EXISTS misty_ai_index_task();
DROP FUNCTION IF EXISTS misty_ai_index_roadmap();
DROP FUNCTION IF EXISTS misty_ai_index_native_calendar();
DROP FUNCTION IF EXISTS misty_ai_index_provider_calendar();

-- The Library lock (a password for Hidden and Recently Deleted) went with the
-- Library extras; Trash is an ordinary collection for every member.
DROP TABLE IF EXISTS library_reauthentication_grants;
DROP TABLE IF EXISTS library_lock_credentials;

-- +goose Down
SELECT 1;
