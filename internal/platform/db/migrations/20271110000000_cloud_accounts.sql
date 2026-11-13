-- +goose Up
-- Accounts on a shared server. The operator account API
-- creates an account with limits and emails a setup key; the key signs it in
-- once. Every limit is NULL (unlimited) unless the operator sets one, so a
-- self-hosted server behaves as before. A suspended account keeps its data.
CREATE TABLE account_controls (
  user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  storage_bytes bigint CHECK (storage_bytes IS NULL OR storage_bytes >= 0),
  devices bigint CHECK (devices IS NULL OR devices >= 0),
  searches_per_month bigint CHECK (searches_per_month IS NULL OR searches_per_month >= 0),
  app_actions_per_month bigint CHECK (app_actions_per_month IS NULL OR app_actions_per_month >= 0),
  suspended_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_controls_suspended_idx ON account_controls(user_id) WHERE suspended_at IS NOT NULL;

-- Setup keys are stored as SHA-256 hashes, are single use and expire.
CREATE TABLE account_setup_keys (
  key_hash text PRIMARY KEY,
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_setup_keys_user_idx ON account_setup_keys(user_id, created_at DESC);

-- Metered use per calendar month (UTC): web searches and app actions.
CREATE TABLE account_usage (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  metric text NOT NULL CHECK (metric IN ('web_searches', 'app_actions')),
  period date NOT NULL,
  used bigint NOT NULL DEFAULT 0 CHECK (used >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, metric, period)
);

ALTER TABLE account_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_controls FORCE ROW LEVEL SECURITY;
CREATE POLICY account_controls_access ON account_controls
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service());

ALTER TABLE account_setup_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_setup_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY account_setup_keys_service ON account_setup_keys
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE account_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY account_usage_access ON account_usage
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service());

-- +goose Down
DROP TABLE IF EXISTS account_usage;
DROP TABLE IF EXISTS account_setup_keys;
DROP TABLE IF EXISTS account_controls;
