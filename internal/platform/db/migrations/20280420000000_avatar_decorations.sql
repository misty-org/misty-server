-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_decoration jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE spaces ADD COLUMN IF NOT EXISTS avatar_decoration jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE spaces DROP COLUMN IF EXISTS avatar_decoration;
ALTER TABLE users DROP COLUMN IF EXISTS avatar_decoration;
