-- +goose Up
-- Device trust and the shared clipboard are gone, and so is the desktop-to-website sign-in
-- handoff. Their tables go; CASCADE takes the device triggers with them. The triggers call the
-- shared misty_notify_account_change, which other tables still use, so it stays. The license
-- row stays as the account's identity, without the old per-device browser license.
DROP TABLE IF EXISTS
    trusted_device_request_nonces,
    device_channel_tickets,
    device_policies,
    device_revoked_keys,
    trusted_devices
    CASCADE;

DROP TABLE IF EXISTS auth_handoff_tokens;

ALTER TABLE licenses DROP COLUMN IF EXISTS license_device;

-- +goose Down
-- The schema comes back empty; what the tables held is not restored.
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS license_device text NOT NULL DEFAULT '';

CREATE TABLE auth_handoff_tokens (
    hashed_token varchar(64) PRIMARY KEY CHECK (char_length(hashed_token) = 64),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_path text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_auth_handoff_tokens_expires_at ON auth_handoff_tokens (expires_at);

CREATE TABLE trusted_devices (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    public_key text NOT NULL,
    key_algorithm text NOT NULL DEFAULT 'ed25519',
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    platform text NOT NULL DEFAULT 'unknown',
    p2p_endpoint_id text,
    device_protocol_versions jsonb NOT NULL DEFAULT '[]'::jsonb,
    admission_state text NOT NULL DEFAULT 'legacy',
    identity_version smallint NOT NULL DEFAULT 1,
    admitted_at timestamptz,
    approved_by_device_id text,
    os_version text NOT NULL DEFAULT '',
    app_version text NOT NULL DEFAULT '',
    session_hash text,
    name_updated_at timestamptz,
    CONSTRAINT trusted_devices_user_id_public_key_key UNIQUE (user_id, public_key),
    CONSTRAINT trusted_devices_admission_state_check CHECK (admission_state IN ('legacy', 'pending', 'admitted', 'revoked')),
    CONSTRAINT trusted_devices_app_version_check CHECK (char_length(app_version) <= 64),
    CONSTRAINT trusted_devices_capabilities_check CHECK (jsonb_typeof(capabilities) = 'object'),
    CONSTRAINT trusted_devices_device_protocol_versions_check CHECK (jsonb_typeof(device_protocol_versions) = 'array'),
    CONSTRAINT trusted_devices_id_check CHECK (id ~ '^device_[0-9a-f-]{36}$'),
    CONSTRAINT trusted_devices_identity_version_check CHECK (identity_version IN (1, 2)),
    CONSTRAINT trusted_devices_key_algorithm_check CHECK (key_algorithm = 'ed25519'),
    CONSTRAINT trusted_devices_name_check CHECK (char_length(name) >= 1 AND char_length(name) <= 100),
    CONSTRAINT trusted_devices_os_version_check CHECK (char_length(os_version) <= 64),
    CONSTRAINT trusted_devices_p2p_endpoint_id_check CHECK (p2p_endpoint_id IS NULL OR p2p_endpoint_id ~ '^[A-Za-z0-9_-]{32,128}$'),
    CONSTRAINT trusted_devices_platform_check CHECK (platform IN ('macos', 'windows', 'linux', 'unknown')),
    CONSTRAINT trusted_devices_public_key_check CHECK (char_length(public_key) >= 32 AND char_length(public_key) <= 4096)
);
CREATE INDEX trusted_devices_user_active_idx ON trusted_devices (user_id, last_seen_at DESC) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX trusted_devices_user_p2p_endpoint_idx ON trusted_devices (user_id, p2p_endpoint_id) WHERE p2p_endpoint_id IS NOT NULL;

CREATE TABLE trusted_device_request_nonces (
    device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    nonce text NOT NULL CONSTRAINT trusted_device_request_nonces_nonce_check
        CHECK (char_length(nonce) >= 16 AND char_length(nonce) <= 200),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, nonce)
);
CREATE INDEX trusted_device_request_nonces_expiry_idx ON trusted_device_request_nonces (expires_at);

CREATE TABLE device_channel_tickets (
    token_hash text PRIMARY KEY CONSTRAINT device_channel_tickets_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    session_hash text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '1 minute')
);
CREATE INDEX device_channel_tickets_device_idx ON device_channel_tickets (device_id);

CREATE TABLE device_policies (
    device_id text PRIMARY KEY REFERENCES trusted_devices(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    policy_version bigint NOT NULL CONSTRAINT device_policies_version_check CHECK (policy_version >= 1),
    payload bytea NOT NULL CONSTRAINT device_policies_payload_check CHECK (octet_length(payload) <= 4096),
    signature text NOT NULL,
    files text NOT NULL CONSTRAINT device_policies_files_check CHECK (files IN ('off', 'view', 'edit')),
    clipboard boolean NOT NULL,
    agent_surfaces jsonb NOT NULL CONSTRAINT device_policies_surfaces_check CHECK (jsonb_typeof(agent_surfaces) = 'array'),
    shared_folders jsonb NOT NULL DEFAULT '[]'::jsonb CONSTRAINT device_policies_folders_check CHECK (jsonb_typeof(shared_folders) = 'array'),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE device_revoked_keys (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key text NOT NULL CONSTRAINT device_revoked_keys_public_key_check
        CHECK (char_length(public_key) >= 32 AND char_length(public_key) <= 128),
    device_id text NOT NULL,
    revoked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, public_key)
);

CREATE TRIGGER device_policy_account_notify AFTER INSERT OR UPDATE ON device_policies
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');
CREATE TRIGGER trusted_device_insert_account_notify AFTER INSERT ON trusted_devices
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');
CREATE TRIGGER trusted_device_identity_account_notify
    AFTER UPDATE OF name, platform, p2p_endpoint_id, revoked_at, admission_state ON trusted_devices
    FOR EACH ROW WHEN (old.name IS DISTINCT FROM new.name OR old.platform IS DISTINCT FROM new.platform
        OR old.p2p_endpoint_id IS DISTINCT FROM new.p2p_endpoint_id OR old.revoked_at IS DISTINCT FROM new.revoked_at
        OR old.admission_state IS DISTINCT FROM new.admission_state)
    EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');

ALTER TABLE auth_handoff_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_handoff_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY auth_handoff_tokens_all_policy ON auth_handoff_tokens
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE trusted_devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE trusted_devices FORCE ROW LEVEL SECURITY;
CREATE POLICY trusted_devices_user_policy ON trusted_devices
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

ALTER TABLE trusted_device_request_nonces ENABLE ROW LEVEL SECURITY;
ALTER TABLE trusted_device_request_nonces FORCE ROW LEVEL SECURITY;
CREATE POLICY trusted_device_request_nonces_user_policy ON trusted_device_request_nonces
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

ALTER TABLE device_channel_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_channel_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY device_channel_tickets_service ON device_channel_tickets
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

ALTER TABLE device_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY device_policies_owner ON device_policies
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

ALTER TABLE device_revoked_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_revoked_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY device_revoked_keys_owner ON device_revoked_keys
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON auth_handoff_tokens, trusted_devices,
            trusted_device_request_nonces, device_channel_tickets, device_policies,
            device_revoked_keys TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
