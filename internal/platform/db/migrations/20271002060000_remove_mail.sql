-- +goose Up
-- Misty no longer reads, drafts or sends provider mail, and the Inbox app is
-- gone. Remove the action audit, the Inbox app's recent-use rows and the mail
-- capability from connected accounts; other capabilities on those accounts stay.
DROP TABLE mail_action_audit;
DELETE FROM user_app_activity WHERE app_id = 'inbox';
UPDATE connected_accounts SET capabilities = capabilities - 'mail' WHERE capabilities ? 'mail';

-- +goose Down
CREATE TABLE mail_action_audit (
    id bigserial PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connection_id text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    action text NOT NULL CHECK (action IN ('thread_modify','draft_create','draft_update','draft_send')),
    target_type text NOT NULL CHECK (target_type IN ('thread','draft')),
    target_id text NOT NULL CHECK (char_length(target_id) BETWEEN 1 AND 320),
    source text NOT NULL CHECK (source IN ('user','ai')),
    confirmed boolean NOT NULL DEFAULT false,
    success boolean NOT NULL,
    error_code text NOT NULL DEFAULT '' CHECK (char_length(error_code) <= 120),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz DEFAULT now(),
    CONSTRAINT mail_action_completion_valid CHECK (completed_at IS NOT NULL OR (NOT success AND error_code = 'mail_operation_pending'))
);
CREATE INDEX mail_action_audit_owner_idx ON mail_action_audit (user_id, created_at DESC, id DESC);
CREATE INDEX mail_action_audit_unfinished_idx ON mail_action_audit (created_at, id) WHERE completed_at IS NULL;
ALTER TABLE mail_action_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_action_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY mail_action_audit_owner ON mail_action_audit
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
GRANT SELECT, INSERT ON TABLE mail_action_audit TO misty_app;
GRANT UPDATE (target_id, success, error_code, completed_at) ON TABLE mail_action_audit TO misty_app;
GRANT SELECT, USAGE ON SEQUENCE mail_action_audit_id_seq TO misty_app;
