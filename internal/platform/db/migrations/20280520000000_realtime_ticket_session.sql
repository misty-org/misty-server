-- +goose Up
-- A realtime ticket remembers the session that asked for it, so ending that session closes its socket.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE realtime_tickets ADD COLUMN IF NOT EXISTS session_id text NOT NULL DEFAULT '';

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE realtime_tickets DROP COLUMN IF EXISTS session_id;
