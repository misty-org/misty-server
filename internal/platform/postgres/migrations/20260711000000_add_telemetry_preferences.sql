-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN analytics_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN error_reporting_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN error_reporting_enabled;
ALTER TABLE users DROP COLUMN analytics_enabled;
-- +goose StatementEnd
