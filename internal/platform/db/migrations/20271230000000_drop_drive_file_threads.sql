-- +goose Up
-- Files have no threads of their own: people talk about a file in the Space's chat, and a thread
-- hangs from any message, about anything.
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS space_drive_threads;

-- +goose Down
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
