-- Sync naming: the account's encrypted sync container is the vault (was the
-- sync "workspace"), and each device sign-in's tree is a workspace (was a
-- tree). A view's page slots are keyed by view node (was tab node), and the
-- flag for devices on the collections tier is uses_collections. Index and
-- constraint names follow. The vault rename runs first so "workspace" is free
-- for trees; Down reverses the order.
-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    pair text[];
BEGIN
    -- This name is at Postgres's 63-byte limit and would be truncated once
    -- renamed; give it a short one first.
    ALTER INDEX IF EXISTS public.browser_sync_tree_receipts_workspace_id_device_id_device_co_key
        RENAME TO browser_sync_tree_receipts_device_counter_key;

    ALTER TABLE public.browser_sync_workspaces RENAME TO browser_sync_vaults;
    ALTER TABLE public.browser_sync_trees RENAME TO browser_sync_workspaces;
    ALTER TABLE public.browser_sync_tree_receipts RENAME TO browser_sync_workspace_receipts;

    FOR r IN
        SELECT table_name FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name LIKE 'browser_sync\_%' AND column_name = 'workspace_id'
    LOOP
        EXECUTE format('ALTER TABLE public.%I RENAME COLUMN workspace_id TO vault_id', r.table_name);
    END LOOP;

    FOR r IN
        SELECT table_name, column_name,
               CASE column_name
                   WHEN 'tree_id' THEN 'workspace_id'
                   WHEN 'tree_mode' THEN 'workspace_mode'
                   WHEN 'tree_version' THEN 'workspace_version'
                   WHEN 'tree_last_counter' THEN 'workspace_last_counter'
                   WHEN 'activation_tree_id' THEN 'activation_workspace_id'
                   WHEN 'tab_node_id' THEN 'view_node_id'
                   WHEN 'cold_records' THEN 'uses_collections'
               END AS new_name
        FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name LIKE 'browser_sync\_%'
          AND column_name IN ('tree_id', 'tree_mode', 'tree_version', 'tree_last_counter',
                              'activation_tree_id', 'tab_node_id', 'cold_records')
    LOOP
        EXECUTE format('ALTER TABLE public.%I RENAME COLUMN %I TO %I', r.table_name, r.column_name, r.new_name);
    END LOOP;

    -- Index and constraint names, one pair at a time so no rename collides.
    -- Index-backed constraints (primary keys, uniques) rename with their index.
    FOREACH pair SLICE 1 IN ARRAY ARRAY[['workspace', 'vault'], ['tree', 'workspace'], ['tab_node', 'view_node'], ['cold_records', 'uses_collections']]
    LOOP
        FOR r IN
            SELECT i.relname AS name
            FROM pg_index x
            JOIN pg_class i ON i.oid = x.indexrelid
            JOIN pg_class t ON t.oid = x.indrelid
            JOIN pg_namespace n ON n.oid = t.relnamespace
            WHERE n.nspname = current_schema() AND t.relname LIKE 'browser_sync\_%'
              AND strpos(i.relname, pair[1]) > 0
        LOOP
            EXECUTE format('ALTER INDEX public.%I RENAME TO %I', r.name, replace(r.name, pair[1], pair[2]));
        END LOOP;
        FOR r IN
            SELECT t.relname AS table_name, c.conname AS name
            FROM pg_constraint c
            JOIN pg_class t ON t.oid = c.conrelid
            JOIN pg_namespace n ON n.oid = t.relnamespace
            WHERE n.nspname = current_schema() AND t.relname LIKE 'browser_sync\_%'
              AND c.contype IN ('c', 'f')
              AND strpos(c.conname, pair[1]) > 0
        LOOP
            EXECUTE format('ALTER TABLE public.%I RENAME CONSTRAINT %I TO %I',
                           r.table_name, r.name, replace(r.name, pair[1], pair[2]));
        END LOOP;
    END LOOP;
END
$$;
-- +goose StatementEnd

COMMENT ON TABLE public.browser_sync_workspaces IS 'Per-device-sign-in workspaces and their sign-in lease holders. The shared workspace (workspace_id = vault_id) is never leased.';
COMMENT ON TABLE public.browser_sync_nodes IS 'AEAD-encrypted workspace nodes. Kind and content are inside the ciphertext.';
COMMENT ON TABLE public.browser_sync_slots IS 'Mutable encrypted slots on workspace nodes: a view''s page (history, page state, session storage) and sign-in shards, padded to size buckets by clients.';

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    pair text[];
BEGIN
    -- Index and constraint names, one pair at a time so no rename collides.
    -- Index-backed constraints (primary keys, uniques) rename with their index.
    FOREACH pair SLICE 1 IN ARRAY ARRAY[['uses_collections', 'cold_records'], ['view_node', 'tab_node'], ['workspace', 'tree'], ['vault', 'workspace']]
    LOOP
        FOR r IN
            SELECT i.relname AS name
            FROM pg_index x
            JOIN pg_class i ON i.oid = x.indexrelid
            JOIN pg_class t ON t.oid = x.indrelid
            JOIN pg_namespace n ON n.oid = t.relnamespace
            WHERE n.nspname = current_schema() AND t.relname LIKE 'browser_sync\_%'
              AND strpos(i.relname, pair[1]) > 0
        LOOP
            EXECUTE format('ALTER INDEX public.%I RENAME TO %I', r.name, replace(r.name, pair[1], pair[2]));
        END LOOP;
        FOR r IN
            SELECT t.relname AS table_name, c.conname AS name
            FROM pg_constraint c
            JOIN pg_class t ON t.oid = c.conrelid
            JOIN pg_namespace n ON n.oid = t.relnamespace
            WHERE n.nspname = current_schema() AND t.relname LIKE 'browser_sync\_%'
              AND c.contype IN ('c', 'f')
              AND strpos(c.conname, pair[1]) > 0
        LOOP
            EXECUTE format('ALTER TABLE public.%I RENAME CONSTRAINT %I TO %I',
                           r.table_name, r.name, replace(r.name, pair[1], pair[2]));
        END LOOP;
    END LOOP;

    FOR r IN
        SELECT table_name, column_name,
               CASE column_name
                   WHEN 'workspace_id' THEN 'tree_id'
                   WHEN 'workspace_mode' THEN 'tree_mode'
                   WHEN 'workspace_version' THEN 'tree_version'
                   WHEN 'workspace_last_counter' THEN 'tree_last_counter'
                   WHEN 'activation_workspace_id' THEN 'activation_tree_id'
                   WHEN 'view_node_id' THEN 'tab_node_id'
                   WHEN 'uses_collections' THEN 'cold_records'
               END AS new_name
        FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name LIKE 'browser_sync\_%'
          AND column_name IN ('workspace_id', 'workspace_mode', 'workspace_version', 'workspace_last_counter',
                              'activation_workspace_id', 'view_node_id', 'uses_collections')
    LOOP
        EXECUTE format('ALTER TABLE public.%I RENAME COLUMN %I TO %I', r.table_name, r.column_name, r.new_name);
    END LOOP;

    FOR r IN
        SELECT table_name FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name LIKE 'browser_sync\_%' AND column_name = 'vault_id'
    LOOP
        EXECUTE format('ALTER TABLE public.%I RENAME COLUMN vault_id TO workspace_id', r.table_name);
    END LOOP;

    ALTER TABLE public.browser_sync_workspace_receipts RENAME TO browser_sync_tree_receipts;
    ALTER TABLE public.browser_sync_workspaces RENAME TO browser_sync_trees;
    ALTER TABLE public.browser_sync_vaults RENAME TO browser_sync_workspaces;
    ALTER INDEX IF EXISTS public.browser_sync_tree_receipts_device_counter_key
        RENAME TO browser_sync_tree_receipts_workspace_id_device_id_device_co_key;
END
$$;
-- +goose StatementEnd

COMMENT ON TABLE public.browser_sync_trees IS 'Per-device workspace trees and their single-driver locks. The shared tree (tree_id = workspace_id) is never driven.';
COMMENT ON TABLE public.browser_sync_nodes IS 'AEAD-encrypted workspace tree nodes. Kind and content are inside the ciphertext.';
COMMENT ON TABLE public.browser_sync_slots IS 'Mutable per-tab encrypted state (history, page state, session storage), padded to size buckets by clients.';
