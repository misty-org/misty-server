-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE media_search_assets (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL CHECK (length(fingerprint) = 64),
    media_type TEXT NOT NULL CHECK (media_type IN ('audio','video')),
    mime_type TEXT NOT NULL,
    duration_ms BIGINT NOT NULL CHECK (duration_ms > 0 AND duration_ms <= 7200000),
    status TEXT NOT NULL DEFAULT 'processing' CHECK (status IN ('processing','indexed','failed')),
    indexed_through_ms BIGINT NOT NULL DEFAULT 0 CHECK (indexed_through_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, asset_id)
);

CREATE TABLE media_search_chunks (
    user_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0 AND chunk_index < 240),
    fingerprint TEXT NOT NULL CHECK (length(fingerprint) = 64),
    start_ms BIGINT NOT NULL CHECK (start_ms >= 0),
    end_ms BIGINT NOT NULL CHECK (end_ms > start_ms AND end_ms <= 7200000),
    status TEXT NOT NULL DEFAULT 'processing' CHECK (status IN ('processing','indexed','failed')),
    failure_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, asset_id, chunk_index),
    FOREIGN KEY (user_id, asset_id) REFERENCES media_search_assets(user_id, asset_id) ON DELETE CASCADE
);

CREATE TABLE media_search_segments (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    start_ms BIGINT NOT NULL CHECK (start_ms >= 0),
    end_ms BIGINT NOT NULL CHECK (end_ms > start_ms AND end_ms <= 7200000),
    segment_kind TEXT NOT NULL CHECK (segment_kind IN ('spoken','visual')),
    content TEXT NOT NULL CHECK (length(content) <= 12000),
    transcript TEXT NOT NULL DEFAULT '',
    visual_description TEXT NOT NULL DEFAULT '',
    visible_text JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    embedding vector(768),
    embedding_model TEXT,
    search_tsv TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, COALESCE(content,'')), 'A') ||
        setweight(to_tsvector('simple'::regconfig, COALESCE(visible_text::text,'')), 'A') ||
        setweight(to_tsvector('simple'::regconfig, COALESCE(metadata::text,'')), 'B')
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (user_id, asset_id, chunk_index) REFERENCES media_search_chunks(user_id, asset_id, chunk_index) ON DELETE CASCADE,
    UNIQUE (user_id, asset_id, segment_kind, start_ms, end_ms)
);

CREATE INDEX media_search_segments_lexical_idx ON media_search_segments USING GIN(search_tsv);
CREATE INDEX media_search_segments_semantic_idx ON media_search_segments USING hnsw(embedding vector_cosine_ops) WHERE embedding IS NOT NULL;
CREATE INDEX media_search_segments_asset_time_idx ON media_search_segments(user_id,asset_id,start_ms);

ALTER TABLE media_search_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE media_search_assets FORCE ROW LEVEL SECURITY;
ALTER TABLE media_search_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE media_search_chunks FORCE ROW LEVEL SECURITY;
ALTER TABLE media_search_segments ENABLE ROW LEVEL SECURITY;
ALTER TABLE media_search_segments FORCE ROW LEVEL SECURITY;
CREATE POLICY media_search_assets_policy ON media_search_assets FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY media_search_chunks_policy ON media_search_chunks FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY media_search_segments_policy ON media_search_segments FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON media_search_assets,media_search_chunks,media_search_segments TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS media_search_segments;
DROP TABLE IF EXISTS media_search_chunks;
DROP TABLE IF EXISTS media_search_assets;
-- +goose StatementEnd
