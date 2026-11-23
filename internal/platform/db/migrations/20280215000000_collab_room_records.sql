-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_apps ADD COLUMN room_generation text NOT NULL DEFAULT ''
  CHECK (room_generation ~ '^[0-9a-f]{0,32}$');

CREATE TABLE space_collab_rooms (
  party text NOT NULL CHECK (party IN ('app-room', 'file-room')),
  room text NOT NULL CHECK (char_length(room) BETWEEN 1 AND 64),
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  resource_id text NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 512),
  app_id text,
  item_id text,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (party, room)
);
CREATE INDEX space_collab_rooms_space_app_idx ON space_collab_rooms (space_id, app_id);
ALTER TABLE space_collab_rooms ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_collab_rooms FORCE ROW LEVEL SECURITY;
CREATE POLICY space_collab_rooms_service ON space_collab_rooms
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE TABLE space_collab_room_users (
  party text NOT NULL,
  room text NOT NULL,
  user_id text NOT NULL,
  app_id text NOT NULL,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (party, room, user_id, app_id),
  FOREIGN KEY (party, room) REFERENCES space_collab_rooms(party, room) ON DELETE CASCADE
);
CREATE INDEX space_collab_room_users_user_idx ON space_collab_room_users (space_id, user_id);
CREATE INDEX space_collab_room_users_app_idx ON space_collab_room_users (space_id, app_id);
ALTER TABLE space_collab_room_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_collab_room_users FORCE ROW LEVEL SECURITY;
CREATE POLICY space_collab_room_users_service ON space_collab_room_users
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE TABLE space_collab_room_control_outbox (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  party text NOT NULL CHECK (party IN ('app-room', 'file-room')),
  room text NOT NULL CHECK (char_length(room) BETWEEN 1 AND 64),
  resource_id text NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 512),
  command text NOT NULL CHECK (command IN ('disconnect', 'purge')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX space_collab_room_control_outbox_pending_idx
  ON space_collab_room_control_outbox (next_attempt_at) WHERE delivered_at IS NULL;
CREATE INDEX space_collab_room_control_outbox_room_idx
  ON space_collab_room_control_outbox (space_id, party, room, command);
ALTER TABLE space_collab_room_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_collab_room_control_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY space_collab_room_control_outbox_service ON space_collab_room_control_outbox
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());
CREATE TRIGGER collab_room_control_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_collab_room_control_outbox
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('room-control');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_collab_rooms, space_collab_room_users,
          space_collab_room_control_outbox TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS space_collab_room_control_outbox;
DROP TABLE IF EXISTS space_collab_room_users;
DROP TABLE IF EXISTS space_collab_rooms;
ALTER TABLE space_apps DROP COLUMN IF EXISTS room_generation;
