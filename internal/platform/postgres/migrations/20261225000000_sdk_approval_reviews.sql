-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
ALTER TABLE agent_run_tool_approvals
 ADD COLUMN sdk_effect_id UUID,
 ADD COLUMN sdk_review_digest TEXT,
 ADD COLUMN sdk_review_ciphertext BYTEA,
 ADD CONSTRAINT sdk_approval_review_complete CHECK (
  (sdk_effect_id IS NULL AND sdk_review_digest IS NULL AND sdk_review_ciphertext IS NULL) OR
  (sdk_effect_id IS NOT NULL AND sdk_review_digest IS NOT NULL AND sdk_review_digest ~ '^[0-9a-f]{64}$' AND sdk_review_ciphertext IS NOT NULL AND octet_length(sdk_review_ciphertext) BETWEEN 32 AND 4194304));
-- +goose StatementEnd
-- +goose Down
-- Retain review and recovery data during rollback.
SELECT 1;
