-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE collab_file_state (
  room_id text PRIMARY KEY REFERENCES collab_rooms(id) ON DELETE CASCADE,
  state jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE collab_file_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_file_state FORCE ROW LEVEL SECURITY;
CREATE POLICY collab_file_state_service ON collab_file_state
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON collab_file_state TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS collab_file_state;
