-- +goose Up
-- +goose StatementBegin
ALTER TABLE space_people
    ADD COLUMN automatic_centroid JSONB,
    ADD COLUMN automatic_sample_count INTEGER NOT NULL DEFAULT 0 CHECK (automatic_sample_count >= 0);
ALTER TABLE space_people ADD CONSTRAINT space_people_automatic_centroid_size CHECK (automatic_centroid IS NULL OR octet_length(automatic_centroid::text) <= 65536);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE space_people DROP CONSTRAINT IF EXISTS space_people_automatic_centroid_size;
ALTER TABLE space_people DROP COLUMN IF EXISTS automatic_sample_count,DROP COLUMN IF EXISTS automatic_centroid;
-- +goose StatementEnd
