-- +goose Up
-- The third-party apps a Space has installed (Misty's own apps are built in and never recorded), and the
-- small JSON each app keeps in a Space. An app is an id the host's catalog knows, pinned to one version;
-- its bundle never passes through the server. The
-- permissions are what a Space mod approved at install, checked by the host on every call. Removing
-- an app deletes its row and its storage, never the Space's files.
CREATE TABLE space_apps (
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  app_id text NOT NULL CHECK (app_id ~ '^[a-z][a-z0-9-]{1,62}$'),
  version text NOT NULL CHECK (char_length(version) BETWEEN 1 AND 64),
  permissions text[] NOT NULL DEFAULT '{}',
  installed_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  installed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_id, app_id)
);
CREATE INDEX space_apps_installer_idx ON space_apps (installed_by);

CREATE TABLE space_app_storage (
  space_id text NOT NULL,
  app_id text NOT NULL,
  key text NOT NULL CHECK (char_length(key) BETWEEN 1 AND 200),
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_id, app_id, key),
  FOREIGN KEY (space_id, app_id) REFERENCES space_apps(space_id, app_id) ON DELETE CASCADE
);

ALTER TABLE space_apps ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_apps FORCE ROW LEVEL SECURITY;
CREATE POLICY space_apps_members ON space_apps
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));
ALTER TABLE space_app_storage ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_app_storage FORCE ROW LEVEL SECURITY;
CREATE POLICY space_app_storage_members ON space_app_storage
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_apps, space_app_storage TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS space_app_storage;
DROP TABLE IF EXISTS space_apps;
