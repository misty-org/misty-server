-- +goose Up
-- Space roles: Discord-style roles a Space's members hold, and per-channel overwrites of them.
-- Every Space has exactly one default role, everyone, at position 0, which every member holds
-- without a row in space_member_roles; only its permissions ever change. Other roles sit at
-- positions 1 and up, higher is above. The owner is above every role and holds every permission.
-- Permission strings are those of internal/spaces/permissions.go; the server checks them, the
-- database only stores them.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_roles (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
  color text NOT NULL DEFAULT '' CHECK (color = '' OR color ~ '^#[0-9a-f]{6}$'),
  position integer NOT NULL,
  hoist boolean NOT NULL DEFAULT false,
  mentionable boolean NOT NULL DEFAULT false,
  permissions text[] NOT NULL DEFAULT '{}',
  is_default boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (CASE WHEN is_default THEN name = 'everyone' AND color = '' AND position = 0 ELSE position >= 1 END),
  -- Deferred, so a reorder or an insert at the bottom can shift every position in one statement.
  CONSTRAINT space_roles_position_key UNIQUE (space_id, position) DEFERRABLE INITIALLY DEFERRED,
  -- The target of the composite keys below, which keep a role inside its own Space.
  CONSTRAINT space_roles_space_role_key UNIQUE (space_id, id)
);
CREATE UNIQUE INDEX space_roles_one_default_idx ON space_roles(space_id) WHERE is_default;

-- A member's roles besides everyone. Leaving or being removed deletes the membership row,
-- and its roles go with it.
CREATE TABLE space_member_roles (
  space_id text NOT NULL,
  user_id text NOT NULL,
  role_id uuid NOT NULL REFERENCES space_roles(id) ON DELETE CASCADE,
  PRIMARY KEY (space_id, user_id, role_id),
  FOREIGN KEY (space_id, user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE,
  FOREIGN KEY (space_id, role_id) REFERENCES space_roles(space_id, id) ON DELETE CASCADE
);
CREATE INDEX space_member_roles_role_idx ON space_member_roles(role_id);

-- A standard channel's overwrites, one per role. DMs and the Space-wide chat have none.
CREATE TABLE space_conversation_overwrites (
  conversation_id text NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
  role_id uuid NOT NULL REFERENCES space_roles(id) ON DELETE CASCADE,
  allow text[] NOT NULL DEFAULT '{}',
  deny text[] NOT NULL DEFAULT '{}',
  PRIMARY KEY (conversation_id, role_id)
);
CREATE INDEX space_conversation_overwrites_role_idx ON space_conversation_overwrites(role_id);

-- Every existing Space gets its everyone role with what members could do before roles
-- (spaces.DefaultEveryonePermissions; the migration test keeps the two lists equal).
INSERT INTO space_roles(space_id, name, position, permissions, is_default)
SELECT s.id, 'everyone', 0, ARRAY['messages.read', 'messages.write', 'attachments.upload',
  'library.view', 'library.upload', 'library.add', 'library.edit', 'library.download',
  'library.import', 'storage.view_own_usage', 'studio.view', 'ask.run']::text[], true
FROM spaces s;

-- The API reaches these tables only as the service, which checks permissions itself.
ALTER TABLE space_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY space_roles_service ON space_roles
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());
ALTER TABLE space_member_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_member_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY space_member_roles_service ON space_member_roles
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());
ALTER TABLE space_conversation_overwrites ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_overwrites FORCE ROW LEVEL SECURITY;
CREATE POLICY space_conversation_overwrites_service ON space_conversation_overwrites
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_roles, space_member_roles, space_conversation_overwrites TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS space_conversation_overwrites;
DROP TABLE IF EXISTS space_member_roles;
DROP TABLE IF EXISTS space_roles;
