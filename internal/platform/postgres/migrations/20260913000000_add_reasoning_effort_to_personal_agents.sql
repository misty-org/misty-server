-- +goose Up
-- +goose StatementBegin
ALTER TABLE personal_agents ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE personal_agents DROP COLUMN reasoning_effort;
-- +goose StatementEnd
