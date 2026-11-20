-- +goose Up
-- Personal, friends and direct messages.
--
-- An account's own Space (is_default) becomes its Personal: private for good, never shared.
-- A direct message is a Space of kind 'direct': its members are the people in the DM, its
-- "everyone" chat is the conversation, and it never shows among the person's Spaces. A one-to-one
-- DM carries the pair in direct_key, so there is one per pair.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE spaces
  ADD COLUMN kind text NOT NULL DEFAULT 'space' CHECK (kind IN ('space', 'direct')),
  ADD COLUMN direct_key text;
CREATE UNIQUE INDEX spaces_direct_key_idx ON spaces (direct_key)
  WHERE direct_key IS NOT NULL AND lifecycle_state = 'active';
ALTER TABLE spaces ADD CONSTRAINT spaces_direct_never_default CHECK (kind = 'space' OR NOT is_default);

-- Something the server tells a person once, on their next sign-in (a Space they were in turned
-- private). Only fixed kinds and names travel.
CREATE TABLE account_notices (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('personal_space_private')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  seen_at timestamptz
);
CREATE INDEX account_notices_unseen_idx ON account_notices (user_id) WHERE seen_at IS NULL;

-- Everyone else leaves every Personal, which is private from now on. Each removed person is told why.
INSERT INTO account_notices (id, user_id, kind, payload)
SELECT 'notice_' || md5(m.space_id || ':' || m.user_id), m.user_id, 'personal_space_private',
       jsonb_build_object('owner_username', owner.username)
FROM space_members m
JOIN spaces s ON s.id = m.space_id AND s.is_default
JOIN users owner ON owner.id = s.owner_user_id
WHERE m.role <> 'owner';
DELETE FROM space_members m USING spaces s
WHERE s.id = m.space_id AND s.is_default AND m.role <> 'owner';
UPDATE space_invitations i SET revoked_at = now()
FROM spaces s
WHERE s.id = i.space_id AND s.is_default AND i.revoked_at IS NULL AND i.consumed_at IS NULL;

-- Friends: one row per pair, user_low < user_high. A request is 'pending' until the other accepts.
CREATE TABLE friendships (
  user_low text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_high text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  state text NOT NULL CHECK (state IN ('pending', 'accepted')),
  requested_by text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  accepted_at timestamptz,
  PRIMARY KEY (user_low, user_high),
  CHECK (user_low < user_high),
  CHECK (requested_by IN (user_low, user_high))
);
CREATE INDEX friendships_high_idx ON friendships (user_high);
CREATE INDEX friendships_requester_idx ON friendships (requested_by, created_at);

-- Every request sent, kept for the daily limit even after it is accepted, ignored or cancelled.
CREATE TABLE friend_request_sends (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  sent_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX friend_request_sends_idx ON friend_request_sends (user_id, sent_at);

CREATE TABLE user_blocks (
  blocker_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blocked_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (blocker_id, blocked_id),
  CHECK (blocker_id <> blocked_id)
);
CREATE INDEX user_blocks_blocked_idx ON user_blocks (blocked_id);

-- Who may send the person a friend request.
CREATE TABLE user_privacy (
  user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  friend_requests text NOT NULL DEFAULT 'everyone' CHECK (friend_requests IN ('everyone', 'shared_spaces')),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- The status a person chose. Online and Idle are worked out from their connections; this keeps
-- only the choice and the custom line.
CREATE TABLE user_status (
  user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  chosen text NOT NULL DEFAULT 'online' CHECK (chosen IN ('online', 'dnd', 'invisible')),
  custom_text text CHECK (custom_text IS NULL OR char_length(custom_text) <= 128),
  custom_emoji text CHECK (custom_emoji IS NULL OR char_length(custom_emoji) <= 32),
  custom_expires_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Pinned messages, in channels, the Space-wide chat and DMs alike.
CREATE TABLE space_message_pins (
  message_id text PRIMARY KEY REFERENCES space_messages(id) ON DELETE CASCADE,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  pinned_by text REFERENCES users(id) ON DELETE SET NULL,
  pinned_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_message_pins_space_idx ON space_message_pins (space_id, pinned_at DESC);

-- How many friend requests the account may send in a day. NULL is unlimited; Misty Cloud sets it.
ALTER TABLE account_controls
  ADD COLUMN friend_requests_per_day bigint CHECK (friend_requests_per_day IS NULL OR friend_requests_per_day >= 0);

-- Marks the one-time move of DMs out of Spaces (DirectMigration), so it runs once.
CREATE TABLE direct_migrations (
  name text PRIMARY KEY,
  finished_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE account_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY account_notices_policy ON account_notices
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id());

ALTER TABLE friendships ENABLE ROW LEVEL SECURITY;
ALTER TABLE friendships FORCE ROW LEVEL SECURITY;
CREATE POLICY friendships_policy ON friendships
  USING (public.misty_rls_is_service() OR public.misty_rls_user_id() IN (user_low, user_high))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_rls_user_id() IN (user_low, user_high));

ALTER TABLE friend_request_sends ENABLE ROW LEVEL SECURITY;
ALTER TABLE friend_request_sends FORCE ROW LEVEL SECURITY;
CREATE POLICY friend_request_sends_policy ON friend_request_sends
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id());

ALTER TABLE user_blocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_blocks FORCE ROW LEVEL SECURITY;
CREATE POLICY user_blocks_policy ON user_blocks
  USING (public.misty_rls_is_service() OR blocker_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR blocker_id = public.misty_rls_user_id());

ALTER TABLE user_privacy ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_privacy FORCE ROW LEVEL SECURITY;
CREATE POLICY user_privacy_policy ON user_privacy
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id());

ALTER TABLE user_status ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_status FORCE ROW LEVEL SECURITY;
CREATE POLICY user_status_policy ON user_status
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id());

ALTER TABLE space_message_pins ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_message_pins FORCE ROW LEVEL SECURITY;
CREATE POLICY space_message_pins_policy ON space_message_pins
  USING (public.misty_rls_is_service())
  WITH CHECK (public.misty_rls_is_service());

ALTER TABLE direct_migrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE direct_migrations FORCE ROW LEVEL SECURITY;
CREATE POLICY direct_migrations_policy ON direct_migrations
  USING (public.misty_rls_is_service())
  WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON account_notices, friendships, friend_request_sends,
          user_blocks, user_privacy, user_status, space_message_pins, direct_migrations TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS direct_migrations;
ALTER TABLE account_controls DROP COLUMN IF EXISTS friend_requests_per_day;
DROP TABLE IF EXISTS space_message_pins;
DROP TABLE IF EXISTS user_status;
DROP TABLE IF EXISTS user_privacy;
DROP TABLE IF EXISTS user_blocks;
DROP TABLE IF EXISTS friend_request_sends;
DROP TABLE IF EXISTS friendships;
DROP TABLE IF EXISTS account_notices;
ALTER TABLE spaces DROP CONSTRAINT IF EXISTS spaces_direct_never_default;
DROP INDEX IF EXISTS spaces_direct_key_idx;
ALTER TABLE spaces DROP COLUMN IF EXISTS direct_key, DROP COLUMN IF EXISTS kind;
