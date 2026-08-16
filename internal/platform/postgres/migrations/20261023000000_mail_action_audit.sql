-- +goose Up
-- +goose StatementBegin
-- Mail action audit records deliberately contain no message content,
-- recipients, subjects, attachment names, or provider response payloads.
CREATE TABLE mail_action_audit (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('thread_modify','draft_create','draft_update','draft_send')),
    target_type TEXT NOT NULL CHECK (target_type IN ('thread','draft')),
    target_id TEXT NOT NULL CHECK (char_length(target_id) BETWEEN 1 AND 320),
    source TEXT NOT NULL CHECK (source IN ('user','ai')),
    confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    success BOOLEAN NOT NULL,
    error_code TEXT NOT NULL DEFAULT '' CHECK (char_length(error_code) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX mail_action_audit_owner_idx
    ON mail_action_audit(user_id,created_at DESC,id DESC);

ALTER TABLE mail_action_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_action_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY mail_action_audit_owner ON mail_action_audit FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT ON mail_action_audit TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE mail_action_audit_id_seq TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: audit history must not be silently removed by a rollback.
SELECT 1;
