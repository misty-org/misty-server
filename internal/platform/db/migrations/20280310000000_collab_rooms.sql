-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE collab_rooms (
  id text PRIMARY KEY CHECK (id ~ '^rm_[a-z2-7]{26}$'),
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  room_key text NOT NULL CHECK (octet_length(room_key) BETWEEN 1 AND 256),
  options jsonb NOT NULL DEFAULT '{}'::jsonb,
  acl_version bigint NOT NULL DEFAULT 0 CHECK (acl_version >= 0),
  purged boolean NOT NULL DEFAULT false,
  generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
  owner_url text NOT NULL DEFAULT '' CHECK (char_length(owner_url) <= 2048),
  owner_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (space_id, room_key)
);

ALTER TABLE collab_rooms ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_rooms FORCE ROW LEVEL SECURITY;
CREATE POLICY collab_rooms_service ON collab_rooms
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON collab_rooms TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS collab_rooms;
