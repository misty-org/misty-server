-- +goose Up
-- +goose StatementBegin
-- Abuse blocks were held only in process memory, so a restart forgave every
-- blocked caller and a second instance never learned about them. Persisting
-- them makes a block survive a deploy and apply across instances.
SET LOCAL lock_timeout = '5s';

CREATE TABLE abuse_blocks (
    -- Hashed identity: "ip:<addr>" or "acct:<digest>". Never a raw credential.
    block_key TEXT PRIMARY KEY CHECK (char_length(block_key) BETWEEN 1 AND 200),
    blocked_until TIMESTAMPTZ NOT NULL,
    -- Retained so a repeat offender's next block escalates from where the last
    -- one left off rather than restarting at the base duration.
    block_seconds INTEGER NOT NULL DEFAULT 60 CHECK (block_seconds > 0),
    reason TEXT NOT NULL DEFAULT '' CHECK (char_length(reason) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Refresh reads only live blocks; expired rows are cleaned up in the same pass.
CREATE INDEX abuse_blocks_active_idx ON abuse_blocks(blocked_until);

-- This table is infrastructure, not Space data: only the service role may see
-- it, and it is deliberately not exposed to the least-privilege app policies
-- that gate member-visible tables.
ALTER TABLE abuse_blocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE abuse_blocks FORCE ROW LEVEL SECURITY;
CREATE POLICY abuse_blocks_service_policy ON abuse_blocks FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON abuse_blocks TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS abuse_blocks CASCADE;
-- +goose StatementEnd
