-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);
CREATE EXTENSION IF NOT EXISTS vector;

-- The pilot originally enforced one folder and 500 rows in the storage layer.
-- Billing still enforces per-request allowances, while the catalog can now scale
-- to multiple roots and whole-disk metadata indexes.
DROP INDEX IF EXISTS smart_library_one_active_folder_per_user;
ALTER TABLE smart_library_folders
    DROP CONSTRAINT IF EXISTS smart_library_folders_user_id_client_library_id_key,
    DROP CONSTRAINT IF EXISTS smart_library_folders_successful_images_check;
ALTER TABLE smart_library_folders
    ADD CONSTRAINT smart_library_folders_successful_images_nonnegative
        CHECK (successful_images >= 0);
CREATE UNIQUE INDEX smart_library_active_client_root
    ON smart_library_folders(user_id, client_library_id)
    WHERE deleted_at IS NULL;

ALTER TABLE smart_library_assets
    ADD COLUMN user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN asset_kind TEXT NOT NULL DEFAULT 'image'
        CHECK (asset_kind IN ('image', 'document', 'text', 'audio', 'archive', 'binary')),
    ADD COLUMN mime_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    ADD COLUMN metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN semantic_embedding vector(768),
    ADD COLUMN embedding_model TEXT,
    ADD COLUMN embedding_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN embedding_input_hash TEXT,
    ADD COLUMN embedded_at TIMESTAMPTZ,
    ADD COLUMN index_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (index_status IN ('pending', 'indexed', 'failed')),
    ADD COLUMN index_failure_code TEXT,
    ADD COLUMN search_tsv TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, COALESCE(description, '')), 'A') ||
        setweight(to_tsvector('simple'::regconfig, COALESCE(tags::text, '')), 'A') ||
        setweight(to_tsvector('simple'::regconfig, COALESCE(metadata::text, '')), 'A') ||
        setweight(to_tsvector('simple'::regconfig, COALESCE(collections::text, '')), 'B')
    ) STORED;

UPDATE smart_library_assets a
SET user_id = f.user_id
FROM smart_library_folders f
WHERE f.id = a.folder_id;
ALTER TABLE smart_library_assets ALTER COLUMN user_id SET NOT NULL;

DROP POLICY IF EXISTS smart_library_assets_policy ON smart_library_assets;
CREATE POLICY smart_library_assets_policy ON smart_library_assets FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

CREATE INDEX smart_library_assets_search_tsv_idx
    ON smart_library_assets USING GIN (search_tsv);
CREATE INDEX smart_library_assets_semantic_hnsw_idx
    ON smart_library_assets USING hnsw (semantic_embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 96)
    WHERE semantic_embedding IS NOT NULL AND status = 'analyzed';
CREATE INDEX smart_library_assets_reindex_idx
    ON smart_library_assets(user_id, folder_id, index_status, updated_at, asset_id);

ALTER TABLE smart_library_cost_events
    ALTER COLUMN folder_id DROP NOT NULL,
    ADD COLUMN event_kind TEXT NOT NULL DEFAULT 'analysis'
        CHECK (event_kind IN ('analysis', 'semantic_index', 'semantic_query', 'reindex'));

CREATE TABLE smart_library_reindex_jobs (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id TEXT REFERENCES smart_library_folders(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'partially_failed', 'failed')),
    embedding_model TEXT NOT NULL,
    embedding_version INTEGER NOT NULL CHECK (embedding_version > 0),
    asset_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    completed_asset_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    failed_asset_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    cursor TEXT NOT NULL DEFAULT '',
    requested_assets INTEGER NOT NULL DEFAULT 0 CHECK (requested_assets >= 0),
    completed_assets INTEGER NOT NULL DEFAULT 0 CHECK (completed_assets >= 0),
    failed_assets INTEGER NOT NULL DEFAULT 0 CHECK (failed_assets >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX smart_library_reindex_jobs_user_status
    ON smart_library_reindex_jobs(user_id, status, created_at DESC);

ALTER TABLE smart_library_reindex_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE smart_library_reindex_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY smart_library_reindex_jobs_policy ON smart_library_reindex_jobs FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON smart_library_reindex_jobs TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS smart_library_reindex_jobs;
DROP INDEX IF EXISTS smart_library_assets_reindex_idx;
DROP INDEX IF EXISTS smart_library_assets_semantic_hnsw_idx;
DROP INDEX IF EXISTS smart_library_assets_search_tsv_idx;
DELETE FROM smart_library_cost_events WHERE folder_id IS NULL;
ALTER TABLE smart_library_cost_events
    DROP COLUMN IF EXISTS event_kind,
    ALTER COLUMN folder_id SET NOT NULL;
DROP POLICY IF EXISTS smart_library_assets_policy ON smart_library_assets;
ALTER TABLE smart_library_assets
    DROP COLUMN IF EXISTS search_tsv,
    DROP COLUMN IF EXISTS index_failure_code,
    DROP COLUMN IF EXISTS index_status,
    DROP COLUMN IF EXISTS embedded_at,
    DROP COLUMN IF EXISTS embedding_input_hash,
    DROP COLUMN IF EXISTS embedding_version,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS semantic_embedding,
    DROP COLUMN IF EXISTS metadata,
    DROP COLUMN IF EXISTS mime_type,
    DROP COLUMN IF EXISTS asset_kind,
    DROP COLUMN IF EXISTS user_id;
CREATE POLICY smart_library_assets_policy ON smart_library_assets FOR ALL
    USING (misty_rls_is_service() OR EXISTS (SELECT 1 FROM smart_library_folders f WHERE f.id = folder_id AND f.user_id = misty_rls_user_id()))
    WITH CHECK (misty_rls_is_service() OR EXISTS (SELECT 1 FROM smart_library_folders f WHERE f.id = folder_id AND f.user_id = misty_rls_user_id()));
DROP INDEX IF EXISTS smart_library_active_client_root;
ALTER TABLE smart_library_folders
    DROP CONSTRAINT IF EXISTS smart_library_folders_successful_images_nonnegative,
    ADD CONSTRAINT smart_library_folders_successful_images_check
        CHECK (successful_images BETWEEN 0 AND 500),
    ADD CONSTRAINT smart_library_folders_user_id_client_library_id_key
        UNIQUE (user_id, client_library_id);
CREATE UNIQUE INDEX smart_library_one_active_folder_per_user
    ON smart_library_folders(user_id) WHERE deleted_at IS NULL;
-- +goose StatementEnd
