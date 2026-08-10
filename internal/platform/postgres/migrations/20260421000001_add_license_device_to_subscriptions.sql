-- +goose Up
-- +goose StatementBegin
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS license_device TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE subscriptions DROP COLUMN IF EXISTS license_device;
-- +goose StatementEnd
