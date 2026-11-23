-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_event_horizon (
  space_id text PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
  purged_through bigint NOT NULL DEFAULT 0 CHECK (purged_through >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE space_event_horizon ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_event_horizon FORCE ROW LEVEL SECURITY;
CREATE POLICY space_event_horizon_service ON space_event_horizon
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE ON space_event_horizon TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS space_event_horizon;
