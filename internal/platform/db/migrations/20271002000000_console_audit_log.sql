-- +goose Up
-- Operator console audit trail. Every mutating console action (revoke sessions,
-- disable an account, restart a service, mint a bootstrap token) is recorded
-- here. Never stores passwords or tokens.
CREATE TABLE console_audit_log (
    id bigserial PRIMARY KEY,
    action text NOT NULL CHECK (char_length(action) BETWEEN 1 AND 80),
    target text NOT NULL DEFAULT '' CHECK (char_length(target) <= 200),
    detail text NOT NULL DEFAULT '' CHECK (char_length(detail) <= 500),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX console_audit_log_recent_idx ON console_audit_log(created_at DESC);

ALTER TABLE console_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE console_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY console_audit_log_service_policy ON console_audit_log
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT,INSERT ON TABLE console_audit_log TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE console_audit_log_id_seq TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE console_audit_log;
