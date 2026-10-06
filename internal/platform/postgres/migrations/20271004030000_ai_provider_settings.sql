-- +goose Up
CREATE TABLE ai_provider_connections (
 id text PRIMARY KEY, owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name text NOT NULL, provider text NOT NULL CHECK(provider IN ('gateway','openai','anthropic','google','openai-compatible')),
 base_url text NOT NULL, ciphertext bytea NOT NULL, nonce bytea NOT NULL,
 revoked boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,owner_user_id)
);
CREATE TABLE ai_model_routes (
 owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE, role text NOT NULL,
 connection_id text, model text NOT NULL DEFAULT '', reasoning text NOT NULL DEFAULT '',
 enabled boolean NOT NULL DEFAULT true, PRIMARY KEY(owner_user_id,role),
 FOREIGN KEY(connection_id,owner_user_id) REFERENCES ai_provider_connections(id,owner_user_id)
);
-- A run freezes public routing references, never credentials. Revoking a
-- connection also stops its in-flight runs; they cannot fall back to Misty's key.
CREATE TABLE ai_model_run_routes (
 owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 run_id text NOT NULL, role text NOT NULL, connection_id text,
 model text NOT NULL, reasoning text NOT NULL, enabled boolean NOT NULL,
 PRIMARY KEY(run_id,role), FOREIGN KEY(connection_id,owner_user_id) REFERENCES ai_provider_connections(id,owner_user_id)
);
ALTER TABLE ai_provider_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_provider_connections FORCE ROW LEVEL SECURITY;
ALTER TABLE ai_model_routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_model_routes FORCE ROW LEVEL SECURITY;
ALTER TABLE ai_model_run_routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_model_run_routes FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_connection_owner ON ai_provider_connections USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY ai_route_owner ON ai_model_routes USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY ai_run_route_owner ON ai_model_run_routes USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON ai_provider_connections,ai_model_routes,ai_model_run_routes TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE ai_model_run_routes;
DROP TABLE ai_model_routes;
DROP TABLE ai_provider_connections;
