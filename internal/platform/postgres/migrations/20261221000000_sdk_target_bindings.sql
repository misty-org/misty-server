-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
CREATE TABLE sdk_backend_connections (
 user_id TEXT NOT NULL,
 id UUID NOT NULL,
 app_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 PRIMARY KEY(user_id,id),
 FOREIGN KEY(user_id,app_id) REFERENCES sdk_app_publishers(user_id,app_id) ON DELETE CASCADE
);
CREATE TABLE sdk_backend_connection_versions (
 user_id TEXT NOT NULL,
 id UUID NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 endpoint_url TEXT NOT NULL CHECK(char_length(endpoint_url) BETWEEN 1 AND 2048),
 bearer_ciphertext BYTEA NOT NULL CHECK(octet_length(bearer_ciphertext)>16),
 key_version INTEGER NOT NULL CHECK(key_version>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,id,revision),
 FOREIGN KEY(user_id,id) REFERENCES sdk_backend_connections(user_id,id) ON DELETE CASCADE
);
CREATE TABLE sdk_targets (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 id UUID NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 PRIMARY KEY(user_id,id)
);
CREATE TABLE sdk_target_versions (
 user_id TEXT NOT NULL,
 id UUID NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 provider_id TEXT NOT NULL,
 provider_version INTEGER NOT NULL,
 app_version TEXT NOT NULL,
 installed_at TIMESTAMPTZ NOT NULL,
 space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
 target JSONB NOT NULL,
 capabilities JSONB NOT NULL CHECK(jsonb_typeof(capabilities)='array'),
 caller_apps JSONB NOT NULL CHECK(jsonb_typeof(caller_apps)='array'),
 connection_id UUID NOT NULL,
 connection_revision INTEGER NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,id,revision),
 FOREIGN KEY(user_id,id) REFERENCES sdk_targets(user_id,id) ON DELETE CASCADE,
 FOREIGN KEY(user_id,provider_id,provider_version) REFERENCES sdk_provider_versions(user_id,provider_id,version),
 FOREIGN KEY(user_id,connection_id,connection_revision) REFERENCES sdk_backend_connection_versions(user_id,id,revision)
);
DO $$ DECLARE name TEXT; BEGIN
 FOREACH name IN ARRAY ARRAY['sdk_backend_connections','sdk_backend_connection_versions','sdk_targets','sdk_target_versions'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY owner_policy ON %I FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id())',name);
  IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
   EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON %I TO misty_app',name);
  END IF;
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Keep connection and target versions for recovery when admissions are disabled.
SELECT 1;
