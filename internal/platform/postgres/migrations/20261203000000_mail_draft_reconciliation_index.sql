-- +goose Up
-- Native draft serialization checks unresolved history before another mutation.
CREATE INDEX mail_action_audit_draft_outcome_idx ON mail_action_audit(connection_id,target_id,user_id)
    WHERE target_type='draft' AND (completed_at IS NULL OR error_code='mail_operation_incomplete' OR (action='draft_send' AND success));

-- +goose Down
-- Forward-only: preserve the lookup needed to prevent replaying ambiguous sends.
SELECT 1;
