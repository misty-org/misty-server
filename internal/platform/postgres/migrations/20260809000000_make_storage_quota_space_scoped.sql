-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_storage_usage (
    space_id TEXT PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
    used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    reserved_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO space_storage_usage(space_id, used_bytes, reserved_bytes)
SELECT s.id,
       COALESCE((
           SELECT SUM(c.logical_bytes)
           FROM space_storage_contributions c
           WHERE c.space_id=s.id AND c.state IN ('active','recovery')
       ), 0),
       COALESCE((
           SELECT SUM(r.reserved_bytes)
           FROM space_upload_reservations r
           WHERE r.space_id=s.id AND r.state='active'
       ), 0)
FROM spaces s;

-- The contributor ledger keeps per-user attribution. The former per-member
-- counter remains as a deprecated rollback artifact but is no longer written
-- or read by the application and does not represent an allowance.

ALTER TABLE space_storage_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_storage_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY space_storage_usage_policy ON space_storage_usage FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_storage_usage TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_storage_usage;
-- +goose StatementEnd
