-- +goose Up
-- Boosts: how many of a member's Boosts go to a Space. A server with Space levels
-- (MISTY_SPACE_LEVELS, Misty Cloud's) raises a Space's limits by the Boosts its current
-- members give it; billing records them through the operator API. A self-hosted server has
-- no levels, and nothing here applies.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_boosts (
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  boosts bigint NOT NULL CHECK (boosts > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_id, user_id)
);
CREATE INDEX space_boosts_user_idx ON space_boosts(user_id);
ALTER TABLE space_boosts ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_boosts FORCE ROW LEVEL SECURITY;
CREATE POLICY space_boosts_service ON space_boosts
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- The largest file an account may add past its Space's level (Misty+'s). NULL is no extra.
ALTER TABLE account_controls
  ADD COLUMN upload_bytes bigint CHECK (upload_bytes IS NULL OR upload_bytes >= 0);

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE account_controls DROP COLUMN upload_bytes;
DROP TABLE IF EXISTS space_boosts;
