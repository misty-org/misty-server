-- +goose Up
-- +goose StatementBegin
-- Avatars now live in object storage (R2) under the avatars/ prefix; Postgres
-- keeps only avatar_version (cache-busting / existence). Drop the raw blob column.
ALTER TABLE users DROP COLUMN IF EXISTS avatar_png;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN avatar_png BYTEA;
-- +goose StatementEnd
