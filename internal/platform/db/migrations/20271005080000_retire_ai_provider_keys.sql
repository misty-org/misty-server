-- +goose Up
-- Every model now runs on Misty's own keys. Accounts keep only which model each
-- sense uses, so saved provider connections and the routes built on them go.
-- Every account starts again from Misty's defaults.
DELETE FROM ai_model_routes;
ALTER TABLE ai_model_routes DROP COLUMN connection_id;
ALTER TABLE ai_model_run_routes DROP COLUMN connection_id;
DROP TABLE ai_provider_connections;

-- A conversation can pin its own model. Empty follows the account's Thinking choice.
ALTER TABLE misty_ask_conversations ADD COLUMN model_override text NOT NULL DEFAULT ''
  CHECK (length(model_override) <= 200);

-- +goose Down
ALTER TABLE misty_ask_conversations DROP COLUMN model_override;
CREATE TABLE ai_provider_connections (
 id text PRIMARY KEY, owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name text NOT NULL, provider text NOT NULL CHECK(provider IN ('gateway','openai','anthropic','google','openai-compatible')),
 base_url text NOT NULL, ciphertext bytea NOT NULL, nonce bytea NOT NULL,
 revoked boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,owner_user_id)
);
ALTER TABLE ai_provider_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_provider_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_connection_owner ON ai_provider_connections USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
ALTER TABLE ai_model_routes ADD COLUMN connection_id text,
 ADD FOREIGN KEY(connection_id,owner_user_id) REFERENCES ai_provider_connections(id,owner_user_id);
ALTER TABLE ai_model_run_routes ADD COLUMN connection_id text,
 ADD FOREIGN KEY(connection_id,owner_user_id) REFERENCES ai_provider_connections(id,owner_user_id);
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON ai_provider_connections TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
