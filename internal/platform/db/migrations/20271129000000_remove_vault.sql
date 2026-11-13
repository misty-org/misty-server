-- +goose Up
-- The vault is gone. Memory stays on each computer and the agent browser's sign-ins stay on the
-- computer they were made on, so nothing replaces it here. Its tables go, with the columns that
-- tied a device to a vault and the grant the vault root signed to admit it.
DROP TABLE IF EXISTS sync_tickets, sync_records, sync_sessions, sync_vaults;

ALTER TABLE trusted_devices
    DROP CONSTRAINT IF EXISTS trusted_devices_grant_payload_check,
    DROP COLUMN IF EXISTS vault_id,
    DROP COLUMN IF EXISTS sync_device_id,
    DROP COLUMN IF EXISTS grant_payload,
    DROP COLUMN IF EXISTS grant_signature;

-- +goose Down
-- The schema comes back empty; what the vaults held is not restored.
ALTER TABLE trusted_devices
    ADD COLUMN vault_id text,
    ADD COLUMN sync_device_id text,
    ADD COLUMN grant_payload bytea,
    ADD COLUMN grant_signature text,
    ADD CONSTRAINT trusted_devices_grant_payload_check
        CHECK (grant_payload IS NULL OR octet_length(grant_payload) <= 4096);

CREATE TABLE sync_vaults (
    vault_id uuid PRIMARY KEY,
    user_id text NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    root_public_key bytea NOT NULL CHECK (octet_length(root_public_key) = 32),
    key_envelope jsonb NOT NULL CHECK (jsonb_typeof(key_envelope) = 'object'),
    key_generation bigint NOT NULL DEFAULT 0 CHECK (key_generation >= 0 AND key_generation <= 9007199254740991),
    key_transition_signature bytea,
    head_sequence bigint NOT NULL DEFAULT 0 CHECK (head_sequence >= 0),
    compacted_through bigint NOT NULL DEFAULT 0 CHECK (compacted_through >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sync_vaults_transition CHECK ((key_generation = 0) = (key_transition_signature IS NULL))
);

CREATE TABLE sync_sessions (
    vault_id uuid NOT NULL REFERENCES sync_vaults(vault_id) ON DELETE CASCADE,
    session_id uuid NOT NULL,
    public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
    grant_signature bytea NOT NULL CHECK (octet_length(grant_signature) = 64),
    name text NOT NULL DEFAULT '',
    platform text NOT NULL DEFAULT '',
    os_version text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    revoked_at timestamptz,
    PRIMARY KEY (vault_id, session_id)
);

CREATE TABLE sync_records (
    vault_id uuid NOT NULL REFERENCES sync_vaults(vault_id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope ~ '^[0-9a-f]{64}$'),
    record_key text NOT NULL CHECK (record_key ~ '^[0-9a-f]{64}$'),
    version bigint NOT NULL CHECK (version > 0),
    sealed bytea NOT NULL CHECK (octet_length(sealed) BETWEEN 28 AND 262144),
    deleted boolean NOT NULL DEFAULT false,
    sequence bigint NOT NULL CHECK (sequence > 0),
    session_id uuid NOT NULL,
    expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, scope, record_key)
);
CREATE UNIQUE INDEX sync_records_sequence ON sync_records (vault_id, sequence);
CREATE INDEX sync_records_droppable ON sync_records (vault_id, updated_at) WHERE deleted OR expires_at IS NOT NULL;

CREATE TABLE sync_tickets (
    token_hash text PRIMARY KEY,
    vault_id uuid NOT NULL,
    session_id uuid NOT NULL,
    session_hash text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL DEFAULT (clock_timestamp() + interval '60 seconds'),
    FOREIGN KEY (vault_id, session_id) REFERENCES sync_sessions(vault_id, session_id) ON DELETE CASCADE
);
