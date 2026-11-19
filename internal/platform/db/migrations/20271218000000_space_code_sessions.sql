-- +goose Up
-- Live code sessions: a Space member goes live on a branch of a repo, and the Space's members
-- edit it together in the session's code room (the collaboration Worker's code-room). The code
-- lives in that room and in each member's clone; this row is the session's record: who started
-- it, which repo and branch, and how it ended. Ending a session purges its room, through the
-- outbox below, the same way a deleted drawing's room is purged.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_code_sessions (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  started_by_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
  -- The normalized origin URL and root commit, so every member's clone matches.
  repo_key text NOT NULL CHECK (char_length(repo_key) BETWEEN 1 AND 512),
  base_commit text NOT NULL CHECK (base_commit ~ '^[0-9a-f]{40}([0-9a-f]{24})?$'),
  branch text NOT NULL CHECK (branch ~ '^misty/live/[a-z0-9][a-z0-9-]{0,62}$'),
  -- Bumped when someone leaves the Space, so the room drops sockets minted before.
  acl_version bigint NOT NULL DEFAULT 1 CHECK (acl_version >= 1),
  created_at timestamptz NOT NULL DEFAULT now(),
  ended_at timestamptz,
  outcome text CHECK (outcome IN ('merged', 'pr', 'kept', 'discarded')),
  CHECK (outcome IS NULL OR ended_at IS NOT NULL)
);
-- One open session per branch of a repo in a Space.
CREATE UNIQUE INDEX space_code_sessions_open_branch_idx
  ON space_code_sessions(space_id, repo_key, branch) WHERE ended_at IS NULL;
CREATE INDEX space_code_sessions_open_idx
  ON space_code_sessions(space_id, created_at DESC) WHERE ended_at IS NULL;
CREATE INDEX space_code_sessions_started_by_idx ON space_code_sessions(started_by_user_id);
ALTER TABLE space_code_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_code_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY space_code_sessions_service ON space_code_sessions
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- Retryable commands for a session's code room: a new ACL version, or the purge when it ends.
CREATE TABLE space_code_session_control_outbox (
  id text PRIMARY KEY,
  session_id text NOT NULL REFERENCES space_code_sessions(id) ON DELETE CASCADE,
  command text NOT NULL CHECK (command IN ('acl', 'purge')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_code_session_control_outbox_pending_idx
  ON space_code_session_control_outbox(next_attempt_at) WHERE delivered_at IS NULL;
CREATE INDEX space_code_session_control_outbox_session_idx
  ON space_code_session_control_outbox(session_id, command);
ALTER TABLE space_code_session_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_code_session_control_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY space_code_session_control_outbox_service ON space_code_session_control_outbox
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());
CREATE TRIGGER code_session_control_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_code_session_control_outbox
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('code-session-control');

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS space_code_session_control_outbox;
DROP TABLE IF EXISTS space_code_sessions;
