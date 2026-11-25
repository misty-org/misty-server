-- +goose Up
-- The operator console is gone from the open server, and nothing else wrote or read its audit trail.
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS console_audit_log;

-- +goose Down
-- The table comes back empty; the old entries are not restored.
SELECT set_config('app.rls_mode', 'service', true);

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
