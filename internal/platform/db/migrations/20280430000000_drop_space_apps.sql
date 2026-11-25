-- +goose Up
-- Apps are installed on each person's computer, never in a Space, so the server keeps no list of
-- them and no permission to manage them. There are no real users yet, so nothing is migrated.
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS space_apps;
UPDATE space_roles SET permissions = array_remove(permissions, 'apps.manage'), updated_at = now()
WHERE 'apps.manage' = ANY(permissions);

-- +goose Down
-- The table comes back empty; what Spaces had installed is not restored.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_apps (
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  app_id text NOT NULL CHECK (app_id ~ '^[a-z][a-z0-9-]{1,62}$'),
  version text NOT NULL CHECK (char_length(version) BETWEEN 1 AND 64),
  permissions text[] NOT NULL DEFAULT '{}',
  installed_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  installed_at timestamptz NOT NULL DEFAULT now(),
  source jsonb CHECK (source IS NULL OR (jsonb_typeof(source) = 'object' AND octet_length(source::text) < 65536)),
  room_generation text NOT NULL DEFAULT '' CHECK (room_generation ~ '^[0-9a-f]{0,32}$'),
  PRIMARY KEY (space_id, app_id)
);
CREATE INDEX space_apps_installer_idx ON space_apps (installed_by);
ALTER TABLE space_apps ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_apps FORCE ROW LEVEL SECURITY;
CREATE POLICY space_apps_members ON space_apps
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_apps TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
