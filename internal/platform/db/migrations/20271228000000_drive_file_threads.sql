-- +goose Up
-- A file's thread: every file in a Space's drive may have one thread in the Space's chat, hung
-- from a message that names the file. This row says which message that is, so every member who
-- opens the file finds the same thread. It goes with the message or the file.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_drive_threads (
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  item_id text NOT NULL REFERENCES space_drive_items(id) ON DELETE CASCADE,
  message_id text NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_id, item_id),
  UNIQUE (message_id)
);

ALTER TABLE space_drive_threads ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drive_threads FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drive_threads_members ON space_drive_threads
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_drive_threads TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS space_drive_threads;
