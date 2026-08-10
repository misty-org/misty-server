-- +goose Up
-- +goose StatementBegin
ALTER TABLE library_files
    ADD COLUMN intrinsic_capture_at TIMESTAMPTZ,
    ADD COLUMN intrinsic_location JSONB;
UPDATE library_files
SET intrinsic_capture_at=CASE
        WHEN intrinsic_metadata->>'capture_timestamp' ~ '^\d{4}-\d{2}-\d{2}T' THEN (intrinsic_metadata->>'capture_timestamp')::timestamptz
        ELSE NULL
    END,
    intrinsic_location=CASE
        WHEN jsonb_typeof(intrinsic_metadata->'embedded_location')='object' THEN intrinsic_metadata->'embedded_location'
        ELSE NULL
    END;
CREATE INDEX library_files_capture_idx ON library_files(intrinsic_capture_at,id) WHERE intrinsic_capture_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_files_capture_idx;
ALTER TABLE library_files DROP COLUMN IF EXISTS intrinsic_location, DROP COLUMN IF EXISTS intrinsic_capture_at;
-- +goose StatementEnd
