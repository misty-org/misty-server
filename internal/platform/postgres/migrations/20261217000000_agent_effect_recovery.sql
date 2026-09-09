-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE agent_toolbox_action_journal
    DROP CONSTRAINT agent_toolbox_action_journal_state_check,
    ADD CONSTRAINT agent_toolbox_action_journal_state_check CHECK(state IN ('started','completed','failed','unknown')),
    ADD COLUMN request_fingerprint TEXT,
    ADD COLUMN result_ciphertext BYTEA;
-- +goose StatementEnd

-- +goose Down
-- Retain uncertain effects and protected results for pinned workers.
SELECT 1;
