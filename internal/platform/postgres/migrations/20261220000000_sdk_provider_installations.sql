-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE sdk_app_publishers (
 user_id TEXT NOT NULL,
 app_id TEXT NOT NULL,
 public_key BYTEA NOT NULL CHECK(octet_length(public_key)=32),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,app_id),
 FOREIGN KEY(user_id,app_id) REFERENCES user_app_installations(user_id,app_id) ON DELETE CASCADE
);
CREATE TABLE sdk_app_manifest_versions (
 user_id TEXT NOT NULL,
 app_id TEXT NOT NULL,
 app_version TEXT NOT NULL,
 digest TEXT NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 document TEXT NOT NULL CHECK(octet_length(document)<=2097152),
 signature BYTEA NOT NULL CHECK(octet_length(signature)=64),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,app_id,app_version),
 FOREIGN KEY(user_id,app_id) REFERENCES sdk_app_publishers(user_id,app_id) ON DELETE CASCADE
);
-- Semantic contracts can be implemented by several providers. Their immutable
-- versions must agree, including schemas and effects, within this installation set.
CREATE TABLE sdk_capability_contract_versions (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0),
 definition JSONB NOT NULL CHECK(jsonb_typeof(definition)='object'),
 PRIMARY KEY(user_id,name,version)
);
CREATE TABLE sdk_provider_versions (
 user_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0),
 app_id TEXT NOT NULL,
 definition JSONB NOT NULL CHECK(jsonb_typeof(definition)='object'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,provider_id,version),
 FOREIGN KEY(user_id,app_id) REFERENCES sdk_app_publishers(user_id,app_id) ON DELETE CASCADE
);
CREATE TABLE sdk_provider_registrations (
 user_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 version INTEGER NOT NULL,
 app_id TEXT NOT NULL,
 app_version TEXT NOT NULL,
 installed_at TIMESTAMPTZ NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 reported_state TEXT NOT NULL DEFAULT 'unavailable' CHECK(reported_state IN ('available','device_required','authentication_required','account_confirmation_required','view_closed','unavailable','revoked')),
 observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 reason TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,provider_id),
 FOREIGN KEY(user_id,provider_id,version) REFERENCES sdk_provider_versions(user_id,provider_id,version),
 FOREIGN KEY(user_id,app_id,app_version) REFERENCES sdk_app_manifest_versions(user_id,app_id,app_version) ON DELETE CASCADE
);

DO $$ DECLARE name TEXT; BEGIN
 FOREACH name IN ARRAY ARRAY['sdk_app_publishers','sdk_app_manifest_versions','sdk_capability_contract_versions','sdk_provider_versions','sdk_provider_registrations'] LOOP
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
-- Versioned manifests and provider identities are recovery records. Roll back
-- admissions through the rollout gate, without dropping pinned versions.
SELECT 1;
