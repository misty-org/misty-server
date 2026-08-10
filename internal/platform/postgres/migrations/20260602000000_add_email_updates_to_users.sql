-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN email_updates_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN email_updates_enabled;
-- +goose StatementEnd
