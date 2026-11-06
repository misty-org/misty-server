-- +goose Up
-- Sync starts over (docs/plans/vault-sync.md). An account has one vault, sealed under a password
-- only its clients know. Everything in it is an opaque record: the server sees scopes and keys
-- that only clients can name, versions, sizes and ciphertext. Workspace trees, seats, leases,
-- signed operation logs, the sign-in slots and vaults per Space are gone, and every existing vault
-- has to be created again; browsing data stays on each computer and uploads into the new one.
DROP TABLE IF EXISTS
    browser_sync_workspace_receipts, browser_sync_slots, browser_sync_nodes, browser_sync_changes,
    browser_sync_workspaces, browser_sync_record_cursors, browser_sync_records, browser_sync_blobs,
    browser_sync_control_requests, browser_sync_connections, browser_sync_instances,
    browser_sync_tickets, browser_sync_receipts, browser_sync_events, browser_sync_restore_runs,
    browser_sync_checkpoints, browser_sync_devices, browser_sync_vaults
    CASCADE;

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

-- One signed-in Misty install in the vault, admitted by a grant the vault root signed.
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
-- Every pull reads in sequence order, across all of the vault's scopes.
CREATE UNIQUE INDEX sync_records_sequence ON sync_records (vault_id, sequence);
-- Compaction looks only at rows that can ever be dropped.
CREATE INDEX sync_records_droppable ON sync_records (vault_id, updated_at) WHERE deleted OR expires_at IS NOT NULL;

CREATE TABLE sync_tickets (
    token_hash text PRIMARY KEY,
    vault_id uuid NOT NULL,
    session_id uuid NOT NULL,
    session_hash text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL DEFAULT (clock_timestamp() + interval '60 seconds'),
    FOREIGN KEY (vault_id, session_id) REFERENCES sync_sessions(vault_id, session_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS sync_tickets, sync_records, sync_sessions, sync_vaults;
