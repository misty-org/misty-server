-- +goose Up
-- +goose StatementBegin
ALTER TABLE smart_library_assets
    ADD COLUMN IF NOT EXISTS counted_success BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS sample_eligible BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE smart_library_assets SET counted_success=TRUE WHERE status='analyzed';
UPDATE smart_library_assets a SET sample_eligible=TRUE
FROM smart_library_batches b
WHERE b.folder_id=a.folder_id AND b.kind='sample' AND b.asset_ids ? a.asset_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE smart_library_assets
    DROP COLUMN IF EXISTS sample_eligible,
    DROP COLUMN IF EXISTS counted_success;
-- +goose StatementEnd
