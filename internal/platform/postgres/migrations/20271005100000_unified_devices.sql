-- +goose Up
-- One device identity for sync, agents and LAN file sharing
-- (docs/design/devices/BRIEF.md). A device is admitted only with a grant
-- signed by the vault root key, which the server can verify but never make.
-- Same-account pairing, its server tickets and per-pair sessions are retired.
ALTER TABLE trusted_devices
    ADD COLUMN admission_state text NOT NULL DEFAULT 'legacy',
    ADD COLUMN identity_version smallint NOT NULL DEFAULT 1,
    ADD COLUMN vault_id text,
    ADD COLUMN sync_device_id text,
    ADD COLUMN grant_payload bytea,
    ADD COLUMN grant_signature text,
    ADD COLUMN admitted_at timestamp with time zone,
    ADD COLUMN approved_by_device_id text,
    ADD COLUMN os_version text NOT NULL DEFAULT '',
    ADD COLUMN app_version text NOT NULL DEFAULT '',
    ADD COLUMN session_hash text,
    ADD COLUMN name_updated_at timestamp with time zone;
ALTER TABLE trusted_devices
    ADD CONSTRAINT trusted_devices_admission_state_check
        CHECK (admission_state IN ('legacy', 'pending', 'admitted', 'revoked')),
    ADD CONSTRAINT trusted_devices_identity_version_check CHECK (identity_version IN (1, 2)),
    ADD CONSTRAINT trusted_devices_os_version_check CHECK (char_length(os_version) <= 64),
    ADD CONSTRAINT trusted_devices_app_version_check CHECK (char_length(app_version) <= 64),
    ADD CONSTRAINT trusted_devices_grant_payload_check
        CHECK (grant_payload IS NULL OR octet_length(grant_payload) <= 4096),
    ADD CONSTRAINT trusted_devices_admitted_grant_check
        CHECK (admission_state <> 'admitted' OR (grant_payload IS NOT NULL AND grant_signature IS NOT NULL AND identity_version = 2));
UPDATE trusted_devices SET admission_state = 'revoked' WHERE revoked_at IS NOT NULL;

-- A key that was ever removed never comes back, under any device id.
CREATE TABLE device_revoked_keys (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key text NOT NULL,
    device_id text NOT NULL,
    revoked_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (user_id, public_key),
    CONSTRAINT device_revoked_keys_public_key_check CHECK (char_length(public_key) BETWEEN 32 AND 128)
);
ALTER TABLE device_revoked_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_revoked_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY device_revoked_keys_owner ON device_revoked_keys
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- The account's current signed device list. Only the latest is kept; its
-- version moves forward by exactly one on every admission and removal.
CREATE TABLE device_lists (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    vault_id text NOT NULL,
    list_version bigint NOT NULL,
    payload bytea NOT NULL,
    signature text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_lists_version_check CHECK (list_version >= 1),
    CONSTRAINT device_lists_payload_check CHECK (octet_length(payload) <= 65536),
    CONSTRAINT device_lists_signature_check CHECK (char_length(signature) BETWEEN 80 AND 100)
);
ALTER TABLE device_lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_lists FORCE ROW LEVEL SECURITY;
CREATE POLICY device_lists_owner ON device_lists
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- Each device signs its own permissions. The server stores and forwards the
-- record for display; the owning device enforces its own copy.
CREATE TABLE device_policies (
    device_id text PRIMARY KEY REFERENCES trusted_devices(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    policy_version bigint NOT NULL,
    payload bytea NOT NULL,
    signature text NOT NULL,
    files text NOT NULL,
    clipboard boolean NOT NULL,
    agent_surfaces jsonb NOT NULL,
    shared_folders jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_policies_version_check CHECK (policy_version >= 1),
    CONSTRAINT device_policies_payload_check CHECK (octet_length(payload) <= 4096),
    CONSTRAINT device_policies_files_check CHECK (files IN ('off', 'view', 'edit')),
    CONSTRAINT device_policies_surfaces_check CHECK (jsonb_typeof(agent_surfaces) = 'array'),
    CONSTRAINT device_policies_folders_check CHECK (jsonb_typeof(shared_folders) = 'array')
);
ALTER TABLE device_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY device_policies_owner ON device_policies
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- Approving a new device from one already added. The server relays the
-- commitment, nonces and sealed vault key but cannot read or forge them.
CREATE TABLE device_admission_requests (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    request_payload bytea NOT NULL,
    request_signature text NOT NULL,
    approver_device_id text REFERENCES trusted_devices(id) ON DELETE CASCADE,
    approver_x25519_public text,
    approver_nonce text,
    requester_nonce text,
    sealed_root text,
    state text DEFAULT 'pending' NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_admission_requests_id_check CHECK (id ~ '^admission_[0-9a-f-]{36}$'),
    CONSTRAINT device_admission_requests_payload_check CHECK (octet_length(request_payload) <= 4096),
    CONSTRAINT device_admission_requests_sealed_check CHECK (sealed_root IS NULL OR char_length(sealed_root) <= 1024),
    CONSTRAINT device_admission_requests_expiry_check CHECK (expires_at <= created_at + interval '10 minutes 5 seconds'),
    CONSTRAINT device_admission_requests_state_check
        CHECK (state IN ('pending', 'challenged', 'revealed', 'approved', 'denied', 'expired'))
);
CREATE UNIQUE INDEX device_admission_requests_open_idx ON device_admission_requests (device_id)
    WHERE state IN ('pending', 'challenged', 'revealed');
ALTER TABLE device_admission_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_admission_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY device_admission_requests_owner ON device_admission_requests
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- One-use, 60-second tickets that open a device channel socket. The socket
-- still has to answer a fresh challenge with the device key.
CREATE TABLE device_channel_tickets (
    token_hash text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    session_hash text NOT NULL DEFAULT '',
    expires_at timestamp with time zone DEFAULT (now() + interval '60 seconds') NOT NULL,
    CONSTRAINT device_channel_tickets_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$')
);
CREATE INDEX device_channel_tickets_device_idx ON device_channel_tickets (device_id);
ALTER TABLE device_channel_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_channel_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY device_channel_tickets_service ON device_channel_tickets
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- A device grant for agent work names the device that asked for it. Device
-- jobs carry it so the target re-checks the signature before it acts.
ALTER TABLE ai_invocation_contexts
    ADD COLUMN run_grant_payload bytea,
    ADD COLUMN run_grant_signature text,
    ADD COLUMN requester_device_id text;
ALTER TABLE ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_run_grant_check
        CHECK (run_grant_payload IS NULL OR octet_length(run_grant_payload) <= 8192);
-- A chat's own device can receive a file another device sends it over the LAN.
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace', 'inbox'));

-- Device and list changes reach every open device of the account.
CREATE TRIGGER device_list_account_notify AFTER INSERT OR UPDATE ON device_lists
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');
CREATE TRIGGER device_policy_account_notify AFTER INSERT OR UPDATE ON device_policies
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');
CREATE TRIGGER device_admission_account_notify AFTER INSERT OR UPDATE ON device_admission_requests
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('device-admission', 'user_id');
DROP TRIGGER IF EXISTS trusted_device_identity_account_notify ON trusted_devices;
CREATE TRIGGER trusted_device_identity_account_notify AFTER UPDATE OF name,platform,p2p_endpoint_id,revoked_at,admission_state ON trusted_devices
FOR EACH ROW WHEN ((OLD.name,OLD.platform,OLD.p2p_endpoint_id,OLD.revoked_at,OLD.admission_state) IS DISTINCT FROM (NEW.name,NEW.platform,NEW.p2p_endpoint_id,NEW.revoked_at,NEW.admission_state))
EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');
CREATE TRIGGER trusted_device_insert_account_notify AFTER INSERT ON trusted_devices
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices', 'user_id');

-- Retired: same-account pairing, server peer tickets and polled presence.
-- Presence and addresses now live in memory on the device channel.
DROP TRIGGER IF EXISTS device_pairing_account_notify ON device_pairing_sessions;
DROP TRIGGER IF EXISTS device_pair_account_notify ON device_pairs;
DROP TRIGGER IF EXISTS device_presence_insert_account_notify ON device_presence;
DROP TRIGGER IF EXISTS device_presence_account_notify ON device_presence;
DROP TABLE IF EXISTS device_pairing_sessions;
DROP TABLE IF EXISTS device_pairs;
DROP TABLE IF EXISTS device_presence;

-- +goose Down
CREATE TABLE public.device_presence (
    device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL,
    p2p_endpoint_id text NOT NULL,
    addressing jsonb DEFAULT '{}'::jsonb NOT NULL,
    protocol_version text NOT NULL,
    connection_hint text DEFAULT 'unknown'::text NOT NULL,
    last_heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (device_id)
);
ALTER TABLE public.device_presence FORCE ROW LEVEL SECURITY;
CREATE TABLE public.device_pairs (
    id text PRIMARY KEY,
    owner_user_id text NOT NULL,
    first_device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    second_device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    state text DEFAULT 'active'::text NOT NULL,
    clipboard_first_to_second boolean DEFAULT false NOT NULL,
    clipboard_second_to_first boolean DEFAULT false NOT NULL,
    first_peer_name text,
    second_peer_name text,
    files_first_accepts_writes boolean DEFAULT false NOT NULL,
    files_second_accepts_writes boolean DEFAULT false NOT NULL,
    confirmed_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE public.device_pairs FORCE ROW LEVEL SECURITY;
CREATE TABLE public.device_pairing_sessions (
    id text PRIMARY KEY,
    owner_user_id text NOT NULL,
    creator_device_id text NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    requester_device_id text REFERENCES trusted_devices(id) ON DELETE CASCADE,
    qr_secret_hash text NOT NULL,
    manual_code_hash text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    failed_attempts integer DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    redeemed_at timestamp with time zone,
    confirmed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE public.device_pairing_sessions FORCE ROW LEVEL SECURITY;
DROP TRIGGER IF EXISTS trusted_device_insert_account_notify ON trusted_devices;
DROP TRIGGER IF EXISTS trusted_device_identity_account_notify ON trusted_devices;
CREATE TRIGGER trusted_device_identity_account_notify AFTER UPDATE OF name,platform,p2p_endpoint_id,revoked_at ON trusted_devices
FOR EACH ROW WHEN ((OLD.name,OLD.platform,OLD.p2p_endpoint_id,OLD.revoked_at) IS DISTINCT FROM (NEW.name,NEW.platform,NEW.p2p_endpoint_id,NEW.revoked_at))
EXECUTE FUNCTION misty_notify_account_change('devices','user_id');
DROP TRIGGER IF EXISTS device_admission_account_notify ON device_admission_requests;
DROP TRIGGER IF EXISTS device_policy_account_notify ON device_policies;
DROP TRIGGER IF EXISTS device_list_account_notify ON device_lists;
DELETE FROM ai_invocation_contexts WHERE kind = 'inbox';
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace'));
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT IF EXISTS ai_invocation_contexts_run_grant_check;
ALTER TABLE ai_invocation_contexts DROP COLUMN IF EXISTS requester_device_id,
    DROP COLUMN IF EXISTS run_grant_signature, DROP COLUMN IF EXISTS run_grant_payload;
DROP TABLE IF EXISTS device_channel_tickets;
DROP TABLE IF EXISTS device_admission_requests;
DROP TABLE IF EXISTS device_policies;
DROP TABLE IF EXISTS device_lists;
DROP TABLE IF EXISTS device_revoked_keys;
ALTER TABLE trusted_devices
    DROP CONSTRAINT IF EXISTS trusted_devices_admitted_grant_check,
    DROP CONSTRAINT IF EXISTS trusted_devices_grant_payload_check,
    DROP CONSTRAINT IF EXISTS trusted_devices_app_version_check,
    DROP CONSTRAINT IF EXISTS trusted_devices_os_version_check,
    DROP CONSTRAINT IF EXISTS trusted_devices_identity_version_check,
    DROP CONSTRAINT IF EXISTS trusted_devices_admission_state_check;
ALTER TABLE trusted_devices
    DROP COLUMN IF EXISTS name_updated_at, DROP COLUMN IF EXISTS session_hash,
    DROP COLUMN IF EXISTS app_version, DROP COLUMN IF EXISTS os_version,
    DROP COLUMN IF EXISTS approved_by_device_id, DROP COLUMN IF EXISTS admitted_at,
    DROP COLUMN IF EXISTS grant_signature, DROP COLUMN IF EXISTS grant_payload,
    DROP COLUMN IF EXISTS sync_device_id, DROP COLUMN IF EXISTS vault_id,
    DROP COLUMN IF EXISTS identity_version, DROP COLUMN IF EXISTS admission_state;
