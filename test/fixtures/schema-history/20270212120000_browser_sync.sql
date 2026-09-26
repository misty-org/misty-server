-- +goose Up
CREATE TABLE browser_sync_workspaces (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL UNIQUE,
    protocol_version INTEGER NOT NULL DEFAULT 1 CHECK (protocol_version = 1),
    key_epoch BIGINT NOT NULL DEFAULT 1 CHECK (key_epoch > 0),
    head_sequence BIGINT NOT NULL DEFAULT 0 CHECK (head_sequence >= 0),
    root_public_key BYTEA NOT NULL CHECK (octet_length(root_public_key) = 32),
    key_envelope JSONB NOT NULL CHECK (jsonb_typeof(key_envelope) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE browser_sync_devices (
    workspace_id UUID NOT NULL REFERENCES browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    device_id UUID NOT NULL,
    public_key BYTEA NOT NULL CHECK (octet_length(public_key) = 32),
    grant_epoch BIGINT NOT NULL CHECK (grant_epoch > 0),
    grant_signature BYTEA NOT NULL CHECK (octet_length(grant_signature) = 64),
    last_counter BIGINT NOT NULL DEFAULT 0 CHECK (last_counter >= 0),
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(workspace_id,device_id)
);
CREATE TABLE browser_sync_events (
    workspace_id UUID NOT NULL REFERENCES browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    operation_id UUID NOT NULL,
    device_id UUID NOT NULL,
    device_counter BIGINT NOT NULL CHECK (device_counter > 0),
    key_epoch BIGINT NOT NULL CHECK (key_epoch > 0),
    envelope JSONB NOT NULL CHECK (jsonb_typeof(envelope) = 'object'),
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(workspace_id,sequence),
    UNIQUE(workspace_id,operation_id),
    UNIQUE(workspace_id,device_id,device_counter),
    FOREIGN KEY(workspace_id,device_id) REFERENCES browser_sync_devices(workspace_id,device_id)
);
-- Receipts survive event compaction. Device high watermarks survive receipt
-- pruning, so an old operation can never become a new write after compaction.
CREATE TABLE browser_sync_receipts (
    workspace_id UUID NOT NULL REFERENCES browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    operation_id UUID NOT NULL,
    sequence BIGINT NOT NULL,
    device_id UUID NOT NULL,
    device_counter BIGINT NOT NULL,
    content_hash BYTEA NOT NULL CHECK (octet_length(content_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(workspace_id,operation_id),
    UNIQUE(workspace_id,device_id,device_counter)
);
CREATE TABLE browser_sync_checkpoints (
    workspace_id UUID NOT NULL REFERENCES browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    key_epoch BIGINT NOT NULL CHECK (key_epoch > 0),
    device_id UUID NOT NULL,
    envelope JSONB NOT NULL CHECK (jsonb_typeof(envelope) = 'object'),
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(workspace_id,sequence)
);
COMMENT ON TABLE browser_sync_events IS 'Opaque endpoint-encrypted changes, ordered by committed server sequence. No decryption keys or browser plaintext.';
COMMENT ON TABLE browser_sync_devices IS 'Workspace-authorized signing identities. Connection presence never grants write authority.';
CREATE TABLE browser_sync_tickets (
    token_hash TEXT PRIMARY KEY,
    workspace_id UUID NOT NULL,
    device_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()+interval '60 seconds',
    FOREIGN KEY(workspace_id,device_id) REFERENCES browser_sync_devices(workspace_id,device_id) ON DELETE CASCADE
);
CREATE INDEX browser_sync_ticket_expiry ON browser_sync_tickets(expires_at);
CREATE TABLE browser_sync_connections (
    connection_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    device_id UUID NOT NULL,
    ready BOOLEAN NOT NULL DEFAULT false,
    applied_sequence BIGINT NOT NULL DEFAULT 0 CHECK(applied_sequence >= 0),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()+interval '45 seconds',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(workspace_id,device_id) REFERENCES browser_sync_devices(workspace_id,device_id) ON DELETE CASCADE
);
CREATE INDEX browser_sync_connection_expiry ON browser_sync_connections(expires_at);
-- +goose Down
-- Preserve user ciphertext and deduplication state on application rollback.
SELECT 1;
