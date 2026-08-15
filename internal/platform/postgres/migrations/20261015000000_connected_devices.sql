-- +goose Up
-- +goose StatementBegin
ALTER TABLE trusted_devices
    ADD COLUMN platform TEXT NOT NULL DEFAULT 'unknown'
        CHECK (platform IN ('macos', 'windows', 'linux', 'ios', 'android', 'unknown')),
    ADD COLUMN p2p_endpoint_id TEXT
        CHECK (p2p_endpoint_id IS NULL OR p2p_endpoint_id ~ '^[A-Za-z0-9_-]{32,128}$'),
    ADD COLUMN device_protocol_versions JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(device_protocol_versions) = 'array');

CREATE UNIQUE INDEX trusted_devices_user_p2p_endpoint_idx
    ON trusted_devices(user_id, p2p_endpoint_id)
    WHERE p2p_endpoint_id IS NOT NULL;

CREATE TABLE device_pairing_sessions (
    id TEXT PRIMARY KEY CHECK (id ~ '^pairing_[0-9a-f-]{36}$'),
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    creator_device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    requester_device_id TEXT REFERENCES trusted_devices(id) ON DELETE CASCADE,
    qr_secret_hash TEXT NOT NULL CHECK (qr_secret_hash ~ '^[0-9a-f]{64}$'),
    manual_code_hash TEXT NOT NULL CHECK (manual_code_hash ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'redeemed', 'confirmed', 'expired', 'locked')),
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 5),
    expires_at TIMESTAMPTZ NOT NULL,
    redeemed_at TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at <= created_at + INTERVAL '5 minutes 5 seconds')
);

CREATE UNIQUE INDEX device_pairing_sessions_live_creator_idx
    ON device_pairing_sessions(creator_device_id)
    WHERE state IN ('pending', 'redeemed');
CREATE INDEX device_pairing_sessions_manual_code_idx
    ON device_pairing_sessions(manual_code_hash)
    WHERE state = 'pending';

CREATE TABLE device_pairs (
    id TEXT PRIMARY KEY CHECK (id ~ '^pair_[0-9a-f-]{36}$'),
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    first_device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    second_device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'revoked')),
    clipboard_first_to_second BOOLEAN NOT NULL DEFAULT FALSE,
    clipboard_second_to_first BOOLEAN NOT NULL DEFAULT FALSE,
    first_peer_name TEXT CHECK (first_peer_name IS NULL OR length(first_peer_name) BETWEEN 1 AND 80),
    second_peer_name TEXT CHECK (second_peer_name IS NULL OR length(second_peer_name) BETWEEN 1 AND 80),
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (first_device_id < second_device_id),
    UNIQUE (owner_user_id, first_device_id, second_device_id)
);

CREATE INDEX device_pairs_device_one_active_idx
    ON device_pairs(first_device_id) WHERE state = 'active';
CREATE INDEX device_pairs_device_two_active_idx
    ON device_pairs(second_device_id) WHERE state = 'active';

CREATE TABLE device_presence (
    device_id TEXT PRIMARY KEY REFERENCES trusted_devices(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    p2p_endpoint_id TEXT NOT NULL CHECK (p2p_endpoint_id ~ '^[A-Za-z0-9_-]{32,128}$'),
    addressing JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(addressing) = 'object'),
    protocol_version TEXT NOT NULL CHECK (protocol_version = 'misty-device/1'),
    connection_hint TEXT NOT NULL DEFAULT 'unknown'
        CHECK (connection_hint IN ('unknown', 'direct', 'relay')),
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX device_presence_owner_online_idx
    ON device_presence(owner_user_id, last_heartbeat_at DESC);

ALTER TABLE device_pairing_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_pairing_sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE device_pairs ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_pairs FORCE ROW LEVEL SECURITY;
ALTER TABLE device_presence ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_presence FORCE ROW LEVEL SECURITY;

CREATE POLICY device_pairing_sessions_owner_policy ON device_pairing_sessions
    FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY device_pairs_owner_policy ON device_pairs
    FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
CREATE POLICY device_presence_owner_policy ON device_presence
    FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON
            device_pairing_sessions, device_pairs, device_presence TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS device_presence, device_pairs, device_pairing_sessions CASCADE;
DROP INDEX IF EXISTS trusted_devices_user_p2p_endpoint_idx;
ALTER TABLE trusted_devices
    DROP COLUMN IF EXISTS device_protocol_versions,
    DROP COLUMN IF EXISTS p2p_endpoint_id,
    DROP COLUMN IF EXISTS platform;
-- +goose StatementEnd
