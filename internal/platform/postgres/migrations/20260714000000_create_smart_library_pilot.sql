-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE smart_library_folders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_library_id TEXT NOT NULL,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('local', 'cloud')),
    state TEXT NOT NULL DEFAULT 'preflight',
    successful_images INTEGER NOT NULL DEFAULT 0 CHECK (successful_images BETWEEN 0 AND 500),
    failed_images INTEGER NOT NULL DEFAULT 0,
    eligible_images INTEGER NOT NULL DEFAULT 0,
    included_images INTEGER NOT NULL DEFAULT 0,
    billable_images INTEGER NOT NULL DEFAULT 0,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, client_library_id)
);
CREATE UNIQUE INDEX smart_library_one_active_folder_per_user
    ON smart_library_folders(user_id) WHERE deleted_at IS NULL;

CREATE SEQUENCE smart_library_result_sequence;

CREATE TABLE smart_library_assets (
    folder_id TEXT NOT NULL REFERENCES smart_library_folders(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    extension TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    modified_bucket BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'analyzed', 'failed')),
    description TEXT,
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    collections JSONB NOT NULL DEFAULT '[]'::jsonb,
    confidence DOUBLE PRECISION,
    failure_code TEXT,
    model TEXT,
    embedding JSONB,
    result_sequence BIGINT UNIQUE,
    analyzed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (folder_id, asset_id)
);
CREATE INDEX smart_library_assets_status ON smart_library_assets(folder_id, status);
CREATE INDEX smart_library_assets_fingerprint ON smart_library_assets(folder_id, fingerprint);

CREATE TABLE smart_library_batches (
    id TEXT PRIMARY KEY,
    folder_id TEXT NOT NULL REFERENCES smart_library_folders(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('sample', 'full', 'rescan', 'organization')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'partially_failed', 'failed')),
    asset_ids JSONB NOT NULL,
    successful_images INTEGER NOT NULL DEFAULT 0,
    failed_images INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE smart_library_cost_events (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES smart_library_folders(id) ON DELETE CASCADE,
    asset_id TEXT,
    batch_id TEXT,
    model TEXT NOT NULL,
    batch_size INTEGER NOT NULL,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    provider_cost_microusd BIGINT NOT NULL DEFAULT 0,
    fallback_reason TEXT,
    success BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE smart_library_folders ENABLE ROW LEVEL SECURITY;
ALTER TABLE smart_library_folders FORCE ROW LEVEL SECURITY;
CREATE POLICY smart_library_folders_policy ON smart_library_folders FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

ALTER TABLE smart_library_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE smart_library_assets FORCE ROW LEVEL SECURITY;
CREATE POLICY smart_library_assets_policy ON smart_library_assets FOR ALL
    USING (misty_rls_is_service() OR EXISTS (SELECT 1 FROM smart_library_folders f WHERE f.id = folder_id AND f.user_id = misty_rls_user_id()))
    WITH CHECK (misty_rls_is_service() OR EXISTS (SELECT 1 FROM smart_library_folders f WHERE f.id = folder_id AND f.user_id = misty_rls_user_id()));

ALTER TABLE smart_library_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE smart_library_batches FORCE ROW LEVEL SECURITY;
CREATE POLICY smart_library_batches_policy ON smart_library_batches FOR ALL
    USING (misty_rls_is_service() OR EXISTS (SELECT 1 FROM smart_library_folders f WHERE f.id = folder_id AND f.user_id = misty_rls_user_id()))
    WITH CHECK (misty_rls_is_service() OR EXISTS (SELECT 1 FROM smart_library_folders f WHERE f.id = folder_id AND f.user_id = misty_rls_user_id()));

ALTER TABLE smart_library_cost_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE smart_library_cost_events FORCE ROW LEVEL SECURITY;
CREATE POLICY smart_library_cost_events_policy ON smart_library_cost_events FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS smart_library_cost_events;
DROP TABLE IF EXISTS smart_library_batches;
DROP TABLE IF EXISTS smart_library_assets;
DROP TABLE IF EXISTS smart_library_folders;
DROP SEQUENCE IF EXISTS smart_library_result_sequence;
-- +goose StatementEnd
