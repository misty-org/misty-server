-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE smart_library_assets
    DROP CONSTRAINT IF EXISTS smart_library_assets_index_status_check;
ALTER TABLE smart_library_assets
    ADD CONSTRAINT smart_library_assets_index_status_check
        CHECK (index_status IN ('pending', 'processing', 'indexed', 'failed')),
    ADD COLUMN index_claim_token TEXT,
    ADD COLUMN index_claimed_at TIMESTAMPTZ;

CREATE INDEX smart_library_assets_active_index_claim_idx
    ON smart_library_assets(user_id, index_claimed_at)
    WHERE index_status = 'processing';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS smart_library_assets_active_index_claim_idx;
UPDATE smart_library_assets SET index_status='pending' WHERE index_status='processing';
ALTER TABLE smart_library_assets
    DROP COLUMN IF EXISTS index_claimed_at,
    DROP COLUMN IF EXISTS index_claim_token,
    DROP CONSTRAINT IF EXISTS smart_library_assets_index_status_check;
ALTER TABLE smart_library_assets
    ADD CONSTRAINT smart_library_assets_index_status_check
        CHECK (index_status IN ('pending', 'indexed', 'failed'));
-- +goose StatementEnd
