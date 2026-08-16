-- +goose Up
-- +goose StatementBegin
CREATE TABLE mcp_remote_connections (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    endpoint_url TEXT NOT NULL CHECK (char_length(endpoint_url) BETWEEN 1 AND 2048),
    transport TEXT NOT NULL DEFAULT 'streamable_http' CHECK (transport='streamable_http'),
    bearer_ciphertext BYTEA NOT NULL DEFAULT ''::bytea,
    bearer_nonce BYTEA NOT NULL DEFAULT ''::bytea,
    key_version INTEGER NOT NULL DEFAULT 1 CHECK (key_version>0),
    status TEXT NOT NULL DEFAULT 'unchecked' CHECK (status IN ('unchecked','active','needs_attention','revoked')),
    last_error_code TEXT NOT NULL DEFAULT '',
    last_checked_at TIMESTAMPTZ,
    last_discovered_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_user_id,endpoint_url)
);
CREATE INDEX mcp_remote_connections_owner_idx ON mcp_remote_connections(owner_user_id,status,updated_at DESC);

CREATE TABLE mcp_discovery_snapshots (
    id TEXT PRIMARY KEY,
    connection_id TEXT NOT NULL REFERENCES mcp_remote_connections(id) ON DELETE CASCADE,
    protocol_version TEXT NOT NULL DEFAULT '',
    server_name TEXT NOT NULL DEFAULT '',
    server_version TEXT NOT NULL DEFAULT '',
    catalog_fingerprint TEXT NOT NULL CHECK (catalog_fingerprint ~ '^[0-9a-f]{64}$'),
    tool_count INTEGER NOT NULL CHECK (tool_count>=0),
    status TEXT NOT NULL CHECK (status IN ('complete','rejected')),
    error_code TEXT NOT NULL DEFAULT '',
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX mcp_discovery_snapshots_connection_idx ON mcp_discovery_snapshots(connection_id,discovered_at DESC);

CREATE TABLE mcp_remote_tools (
    id TEXT PRIMARY KEY,
    connection_id TEXT NOT NULL REFERENCES mcp_remote_connections(id) ON DELETE CASCADE,
    remote_name TEXT NOT NULL CHECK (char_length(remote_name) BETWEEN 1 AND 240),
    stable_name TEXT NOT NULL CHECK (char_length(stable_name) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description)<=4000),
    input_schema JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(input_schema)='object'),
    schema_fingerprint TEXT NOT NULL CHECK (schema_fingerprint ~ '^[0-9a-f]{64}$'),
    schema_status TEXT NOT NULL CHECK (schema_status IN ('valid','unsupported')),
    disabled_reason TEXT NOT NULL DEFAULT '',
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(connection_id,remote_name),
    UNIQUE(connection_id,stable_name),
    UNIQUE(connection_id,id)
);
CREATE INDEX mcp_remote_tools_connection_idx ON mcp_remote_tools(connection_id,removed_at,schema_status,remote_name);

CREATE TABLE personal_agent_mcp_tools (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES mcp_remote_connections(id) ON DELETE CASCADE,
    remote_tool_id TEXT NOT NULL,
    stable_name TEXT NOT NULL,
    schema_fingerprint TEXT NOT NULL CHECK (schema_fingerprint ~ '^[0-9a-f]{64}$'),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_id,connection_id,remote_tool_id),
    UNIQUE(agent_id,stable_name),
    FOREIGN KEY(connection_id,remote_tool_id) REFERENCES mcp_remote_tools(connection_id,id) ON DELETE CASCADE
);
CREATE INDEX personal_agent_mcp_tools_agent_idx ON personal_agent_mcp_tools(agent_id,enabled,stable_name);

CREATE TABLE mcp_tool_execution_audit (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES mcp_remote_connections(id) ON DELETE CASCADE,
    remote_tool_id TEXT,
    remote_name TEXT NOT NULL,
    stable_name TEXT NOT NULL,
    run_id TEXT,
    idempotency_key TEXT NOT NULL,
    source TEXT NOT NULL,
    approved BOOLEAN NOT NULL,
    success BOOLEAN NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms>=0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_user_id,idempotency_key),
    FOREIGN KEY(connection_id,remote_tool_id) REFERENCES mcp_remote_tools(connection_id,id)
);
CREATE INDEX mcp_tool_execution_audit_agent_idx ON mcp_tool_execution_audit(agent_id,created_at DESC);

ALTER TABLE mcp_remote_connections ENABLE ROW LEVEL SECURITY; ALTER TABLE mcp_remote_connections FORCE ROW LEVEL SECURITY;
ALTER TABLE mcp_discovery_snapshots ENABLE ROW LEVEL SECURITY; ALTER TABLE mcp_discovery_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE mcp_remote_tools ENABLE ROW LEVEL SECURITY; ALTER TABLE mcp_remote_tools FORCE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_mcp_tools ENABLE ROW LEVEL SECURITY; ALTER TABLE personal_agent_mcp_tools FORCE ROW LEVEL SECURITY;
ALTER TABLE mcp_tool_execution_audit ENABLE ROW LEVEL SECURITY; ALTER TABLE mcp_tool_execution_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY mcp_remote_connections_owner ON mcp_remote_connections FOR ALL
    USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY mcp_discovery_snapshots_owner_read ON mcp_discovery_snapshots FOR SELECT
    USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM mcp_remote_connections c WHERE c.id=connection_id AND c.owner_user_id=misty_rls_user_id()));
CREATE POLICY mcp_discovery_snapshots_service_write ON mcp_discovery_snapshots FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY mcp_remote_tools_owner_read ON mcp_remote_tools FOR SELECT
    USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM mcp_remote_connections c WHERE c.id=connection_id AND c.owner_user_id=misty_rls_user_id()));
CREATE POLICY mcp_remote_tools_service_write ON mcp_remote_tools FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY personal_agent_mcp_tools_owner ON personal_agent_mcp_tools FOR ALL
    USING (misty_rls_is_service() OR (
        owner_user_id=misty_rls_user_id()
        AND EXISTS(SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id())
        AND EXISTS(SELECT 1 FROM mcp_remote_connections c WHERE c.id=connection_id AND c.owner_user_id=misty_rls_user_id())
        AND EXISTS(SELECT 1 FROM mcp_remote_tools t WHERE t.id=remote_tool_id AND t.connection_id=connection_id)
    ))
    WITH CHECK (misty_rls_is_service() OR (
        owner_user_id=misty_rls_user_id()
        AND EXISTS(SELECT 1 FROM personal_agents a WHERE a.id=agent_id AND a.owner_user_id=misty_rls_user_id())
        AND EXISTS(SELECT 1 FROM mcp_remote_connections c WHERE c.id=connection_id AND c.owner_user_id=misty_rls_user_id())
        AND EXISTS(SELECT 1 FROM mcp_remote_tools t WHERE t.id=remote_tool_id AND t.connection_id=connection_id)
    ));
CREATE POLICY mcp_tool_execution_audit_owner_read ON mcp_tool_execution_audit FOR SELECT
    USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY mcp_tool_execution_audit_service_write ON mcp_tool_execution_audit FOR INSERT
    WITH CHECK (misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON mcp_remote_connections,mcp_discovery_snapshots,mcp_remote_tools,
            personal_agent_mcp_tools,mcp_tool_execution_audit TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: connector provenance and content-free execution audit must be retained.
SELECT 1;
-- +goose StatementEnd
