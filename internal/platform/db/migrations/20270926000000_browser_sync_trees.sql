-- Per-device workspace trees. Each enrolled device owns one tree (tree_id =
-- device_id); account-wide groups/websites live in the shared tree (tree_id =
-- workspace_id). A device drives at most one tree and a tree has at most one
-- driver. Content stays endpoint-encrypted: the server sees node IDs, parent
-- links, versions and ciphertext hashes, never node kinds or plaintext.
-- +goose Up
ALTER TABLE public.browser_sync_workspaces
    ADD COLUMN tree_mode boolean NOT NULL DEFAULT false;

-- Tree ops and claims use their own per-device counter, independent of the
-- credential log's, so neither outbox waits on the other.
ALTER TABLE public.browser_sync_devices
    ADD COLUMN os_version text NOT NULL DEFAULT '',
    ADD COLUMN activation_tree_id uuid,
    ADD COLUMN tree_last_counter bigint NOT NULL DEFAULT 0 CHECK (tree_last_counter >= 0);

ALTER TABLE public.browser_sync_control_requests
    ADD COLUMN tree_id uuid;

CREATE TABLE public.browser_sync_trees (
    workspace_id uuid NOT NULL REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    tree_id uuid NOT NULL,
    driver_device_id uuid,
    driver_epoch uuid,
    driver_seen_at timestamp with time zone,
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    head_sequence bigint NOT NULL DEFAULT 0 CHECK (head_sequence >= 0),
    PRIMARY KEY (workspace_id, tree_id),
    CONSTRAINT browser_sync_tree_driver_pair CHECK ((driver_device_id IS NULL) = (driver_epoch IS NULL)),
    FOREIGN KEY (workspace_id, driver_device_id) REFERENCES public.browser_sync_devices(workspace_id, device_id)
);
-- One tree per device: a device can never drive two trees (no loopback).
CREATE UNIQUE INDEX browser_sync_tree_one_driver ON public.browser_sync_trees(workspace_id, driver_device_id) WHERE driver_device_id IS NOT NULL;
COMMENT ON TABLE public.browser_sync_trees IS 'Per-device workspace trees and their single-driver locks. The shared tree (tree_id = workspace_id) is never driven.';

CREATE TABLE public.browser_sync_nodes (
    workspace_id uuid NOT NULL,
    tree_id uuid NOT NULL,
    node_id uuid NOT NULL,
    parent_id uuid,
    version bigint NOT NULL CHECK (version > 0),
    key_epoch bigint NOT NULL CHECK (key_epoch > 0),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 28 AND 65536),
    content_hash bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    updated_seq bigint NOT NULL,
    PRIMARY KEY (workspace_id, tree_id, node_id),
    FOREIGN KEY (workspace_id, tree_id) REFERENCES public.browser_sync_trees(workspace_id, tree_id) ON DELETE CASCADE
);
CREATE INDEX browser_sync_nodes_parent ON public.browser_sync_nodes(workspace_id, tree_id, parent_id);
COMMENT ON TABLE public.browser_sync_nodes IS 'AEAD-encrypted workspace tree nodes. Kind and content are inside the ciphertext.';

CREATE TABLE public.browser_sync_slots (
    workspace_id uuid NOT NULL,
    tree_id uuid NOT NULL,
    tab_node_id uuid NOT NULL,
    slot_kind smallint NOT NULL CHECK (slot_kind BETWEEN 1 AND 16),
    version bigint NOT NULL CHECK (version > 0),
    key_epoch bigint NOT NULL CHECK (key_epoch > 0),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 28 AND 1048576),
    content_hash bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    updated_seq bigint NOT NULL,
    PRIMARY KEY (workspace_id, tree_id, tab_node_id, slot_kind),
    FOREIGN KEY (workspace_id, tree_id, tab_node_id) REFERENCES public.browser_sync_nodes(workspace_id, tree_id, node_id) ON DELETE CASCADE
);
COMMENT ON TABLE public.browser_sync_slots IS 'Mutable per-tab encrypted state (history, page state, session storage), padded to size buckets by clients.';

-- Change feed: signed operation manifests (IDs, versions, ciphertext hashes).
-- Content is fetched from nodes; old entries are pruned and lagging clients
-- fall back to a subtree snapshot.
CREATE TABLE public.browser_sync_changes (
    workspace_id uuid NOT NULL,
    tree_id uuid NOT NULL,
    tree_version bigint NOT NULL CHECK (tree_version > 0),
    sequence bigint NOT NULL CHECK (sequence > 0),
    operation_id uuid NOT NULL,
    device_id uuid NOT NULL,
    device_counter bigint NOT NULL CHECK (device_counter > 0),
    key_epoch bigint NOT NULL CHECK (key_epoch > 0),
    merkle_root bytea NOT NULL CHECK (octet_length(merkle_root) = 32),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    signature bytea NOT NULL CHECK (octet_length(signature) = 64),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, tree_id, tree_version),
    UNIQUE (workspace_id, sequence),
    FOREIGN KEY (workspace_id, tree_id) REFERENCES public.browser_sync_trees(workspace_id, tree_id) ON DELETE CASCADE
);
CREATE INDEX browser_sync_changes_created ON public.browser_sync_changes(created_at);

-- Deduplication for tree ops and claims. Kept apart from the credential log's
-- receipts because the two counters are independent.
CREATE TABLE public.browser_sync_tree_receipts (
    workspace_id uuid NOT NULL REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    sequence bigint NOT NULL,
    device_id uuid NOT NULL,
    device_counter bigint NOT NULL,
    content_hash bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    discarded boolean NOT NULL DEFAULT false,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, operation_id),
    UNIQUE (workspace_id, device_id, device_counter)
);

CREATE TABLE public.browser_sync_blobs (
    workspace_id uuid NOT NULL REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    blob_hash bytea NOT NULL CHECK (octet_length(blob_hash) = 32),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 28 AND 262144),
    last_ref_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, blob_hash)
);
CREATE INDEX browser_sync_blobs_last_ref ON public.browser_sync_blobs(last_ref_at);
COMMENT ON TABLE public.browser_sync_blobs IS 'Content-addressed encrypted favicons. blob_hash is an HMAC under an account key, so identical content never dedupes across accounts.';

-- Page-state restore runs: IDs and step counts only, never page content.
-- Enforces the per-run step cap and the per-user daily cap.
CREATE TABLE public.browser_sync_restore_runs (
    restore_id uuid PRIMARY KEY,
    user_id text NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    steps integer NOT NULL DEFAULT 0 CHECK (steps >= 0),
    created_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE INDEX browser_sync_restore_runs_user ON public.browser_sync_restore_runs(user_id, created_at);

-- Every existing device initially drives its own tree; the shared tree is undriven.
INSERT INTO public.browser_sync_trees(workspace_id, tree_id)
SELECT workspace_id, workspace_id FROM public.browser_sync_workspaces
ON CONFLICT DO NOTHING;
INSERT INTO public.browser_sync_trees(workspace_id, tree_id, driver_device_id, driver_epoch, driver_seen_at)
SELECT workspace_id, device_id, CASE WHEN revoked_at IS NULL THEN device_id END, CASE WHEN revoked_at IS NULL THEN gen_random_uuid() END, NULL
FROM public.browser_sync_devices
ON CONFLICT DO NOTHING;

-- +goose Down
-- Retain encrypted trees on application rollback; v1 clients ignore them.
SELECT 1;
