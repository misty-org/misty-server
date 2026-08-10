-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN avatar_png BYTEA;
ALTER TABLE users ADD COLUMN avatar_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN avatar_updated_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN avatar_updated_at;
ALTER TABLE users DROP COLUMN avatar_version;
ALTER TABLE users DROP COLUMN avatar_png;
-- +goose StatementEnd
