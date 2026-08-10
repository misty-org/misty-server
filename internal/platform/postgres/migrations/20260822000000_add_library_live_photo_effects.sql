-- +goose Up
ALTER TABLE space_library_asset_stacks
    ADD COLUMN effect TEXT NOT NULL DEFAULT 'still'
    CHECK (effect IN ('still','loop','bounce','long_exposure'));

-- +goose Down
ALTER TABLE space_library_asset_stacks DROP COLUMN IF EXISTS effect;
