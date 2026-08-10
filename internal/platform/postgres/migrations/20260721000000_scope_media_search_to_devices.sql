-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE media_search_devices (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL CHECK (device_id ~ '^device_[0-9a-f]{32}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, device_id)
);

ALTER TABLE media_search_segments
    DROP CONSTRAINT media_search_segments_user_id_asset_id_chunk_index_fkey,
    DROP CONSTRAINT media_search_segments_user_id_asset_id_segment_kind_start_m_key;
ALTER TABLE media_search_chunks
    DROP CONSTRAINT media_search_chunks_user_id_asset_id_fkey,
    DROP CONSTRAINT media_search_chunks_pkey;
ALTER TABLE media_search_assets DROP CONSTRAINT media_search_assets_pkey;

ALTER TABLE media_search_assets ADD COLUMN device_id TEXT NOT NULL DEFAULT 'device_00000000000000000000000000000000';
ALTER TABLE media_search_chunks ADD COLUMN device_id TEXT NOT NULL DEFAULT 'device_00000000000000000000000000000000';
ALTER TABLE media_search_segments ADD COLUMN device_id TEXT NOT NULL DEFAULT 'device_00000000000000000000000000000000';

INSERT INTO media_search_devices(user_id, device_id, created_at, last_seen_at)
SELECT user_id, device_id, MIN(created_at), MAX(updated_at)
FROM media_search_assets
GROUP BY user_id, device_id
ON CONFLICT (user_id, device_id) DO NOTHING;

ALTER TABLE media_search_assets
    ADD CONSTRAINT media_search_assets_device_id_check CHECK (device_id ~ '^device_[0-9a-f]{32}$'),
    ADD PRIMARY KEY (user_id, device_id, asset_id),
    ADD CONSTRAINT media_search_assets_user_id_device_id_fkey
        FOREIGN KEY (user_id, device_id) REFERENCES media_search_devices(user_id, device_id) ON DELETE CASCADE;
ALTER TABLE media_search_chunks
    ADD CONSTRAINT media_search_chunks_device_id_check CHECK (device_id ~ '^device_[0-9a-f]{32}$'),
    ADD PRIMARY KEY (user_id, device_id, asset_id, chunk_index),
    ADD CONSTRAINT media_search_chunks_user_id_device_id_asset_id_fkey
        FOREIGN KEY (user_id, device_id, asset_id) REFERENCES media_search_assets(user_id, device_id, asset_id) ON DELETE CASCADE;
ALTER TABLE media_search_segments
    ADD CONSTRAINT media_search_segments_device_id_check CHECK (device_id ~ '^device_[0-9a-f]{32}$'),
    ADD CONSTRAINT media_search_segments_user_id_device_id_asset_id_chunk_fkey
        FOREIGN KEY (user_id, device_id, asset_id, chunk_index)
        REFERENCES media_search_chunks(user_id, device_id, asset_id, chunk_index) ON DELETE CASCADE,
    ADD CONSTRAINT media_search_segments_device_asset_kind_time_key
        UNIQUE (user_id, device_id, asset_id, segment_kind, start_ms, end_ms);

DROP INDEX media_search_segments_asset_time_idx;
CREATE INDEX media_search_segments_device_asset_time_idx
    ON media_search_segments(user_id, device_id, asset_id, start_ms);
CREATE INDEX media_search_segments_device_idx
    ON media_search_segments(user_id, device_id);

ALTER TABLE media_search_devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE media_search_devices FORCE ROW LEVEL SECURITY;
CREATE POLICY media_search_devices_policy ON media_search_devices FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON media_search_devices TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM media_search_assets
        GROUP BY user_id, asset_id HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot remove media device scoping while duplicate cross-device asset ids exist';
    END IF;
END $$;

DROP INDEX media_search_segments_device_idx;
DROP INDEX media_search_segments_device_asset_time_idx;

ALTER TABLE media_search_segments
    DROP CONSTRAINT media_search_segments_user_id_device_id_asset_id_chunk_fkey,
    DROP CONSTRAINT media_search_segments_device_asset_kind_time_key,
    DROP CONSTRAINT media_search_segments_device_id_check;
ALTER TABLE media_search_chunks
    DROP CONSTRAINT media_search_chunks_user_id_device_id_asset_id_fkey,
    DROP CONSTRAINT media_search_chunks_device_id_check,
    DROP CONSTRAINT media_search_chunks_pkey;
ALTER TABLE media_search_assets
    DROP CONSTRAINT media_search_assets_user_id_device_id_fkey,
    DROP CONSTRAINT media_search_assets_device_id_check,
    DROP CONSTRAINT media_search_assets_pkey;

ALTER TABLE media_search_segments DROP COLUMN device_id;
ALTER TABLE media_search_chunks DROP COLUMN device_id;
ALTER TABLE media_search_assets DROP COLUMN device_id;

ALTER TABLE media_search_assets
    ADD CONSTRAINT media_search_assets_pkey PRIMARY KEY (user_id, asset_id);
ALTER TABLE media_search_chunks
    ADD CONSTRAINT media_search_chunks_pkey PRIMARY KEY (user_id, asset_id, chunk_index),
    ADD CONSTRAINT media_search_chunks_user_id_asset_id_fkey
        FOREIGN KEY (user_id, asset_id) REFERENCES media_search_assets(user_id, asset_id) ON DELETE CASCADE;
ALTER TABLE media_search_segments
    ADD CONSTRAINT media_search_segments_user_id_asset_id_chunk_index_fkey
        FOREIGN KEY (user_id, asset_id, chunk_index)
        REFERENCES media_search_chunks(user_id, asset_id, chunk_index) ON DELETE CASCADE,
    ADD CONSTRAINT media_search_segments_user_id_asset_id_segment_kind_start_m_key
        UNIQUE (user_id, asset_id, segment_kind, start_ms, end_ms);

CREATE INDEX media_search_segments_asset_time_idx
    ON media_search_segments(user_id, asset_id, start_ms);
DROP TABLE media_search_devices;
-- +goose StatementEnd
