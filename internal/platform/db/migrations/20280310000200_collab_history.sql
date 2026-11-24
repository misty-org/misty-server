-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE collab_history (
  id text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
  room_id text NOT NULL REFERENCES collab_rooms(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL,
  byte_size integer NOT NULL CHECK (byte_size BETWEEN 0 AND 8388608),
  checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
  made_by text NOT NULL DEFAULT '' CHECK (char_length(made_by) <= 256),
  state bytea NOT NULL
);
CREATE INDEX collab_history_room_idx ON collab_history (room_id, created_at, id);

ALTER TABLE collab_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_history FORCE ROW LEVEL SECURITY;
CREATE POLICY collab_history_service ON collab_history
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON collab_history TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS collab_history;
