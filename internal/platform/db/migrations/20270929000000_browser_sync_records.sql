-- Pulled sync tier: endpoint-encrypted records (bookmarks, tab groups, and
-- browsing history in hourly per-device batches) kept out of the live tree
-- protocol. Nothing is pushed: clients pull
-- a collection on open and focus, and when a hint says its cursor moved. The
-- server sees a collection name, an opaque keyed record id, a version and
-- ciphertext; never titles, URLs or folder structure.
-- +goose Up
CREATE TABLE public.browser_sync_records (
    workspace_id uuid NOT NULL REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    collection text NOT NULL CHECK (collection IN ('bookmarks', 'tab_groups', 'history')),
    record_key text NOT NULL CHECK (record_key ~ '^[0-9a-f]{64}$'),
    version bigint NOT NULL CHECK (version > 0),
    -- NULL marks a deletion, kept so other devices learn of it.
    ciphertext bytea CHECK (ciphertext IS NULL OR octet_length(ciphertext) <= 65536),
    sequence bigint NOT NULL CHECK (sequence > 0),
    device_id uuid NOT NULL,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, collection, record_key)
);
CREATE INDEX browser_sync_records_feed ON public.browser_sync_records(workspace_id, collection, sequence);

-- Per collection: the latest sequence, and the floor below which deletions
-- were compacted away (a client behind it re-reads the whole collection).
CREATE TABLE public.browser_sync_record_cursors (
    workspace_id uuid NOT NULL REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE,
    collection text NOT NULL CHECK (collection IN ('bookmarks', 'tab_groups', 'history')),
    sequence bigint NOT NULL DEFAULT 0 CHECK (sequence >= 0),
    compacted_through bigint NOT NULL DEFAULT 0 CHECK (compacted_through >= 0),
    PRIMARY KEY (workspace_id, collection)
);

-- A device that has pulled a collection keeps its bookmarks there. Once every
-- active device has, the shared tree (where bookmarks used to live) retires.
ALTER TABLE public.browser_sync_devices
    ADD COLUMN cold_records boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE public.browser_sync_devices DROP COLUMN cold_records;
DROP TABLE public.browser_sync_record_cursors;
DROP TABLE public.browser_sync_records;
