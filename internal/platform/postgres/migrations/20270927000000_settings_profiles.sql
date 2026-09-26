CREATE TABLE IF NOT EXISTS settings_profiles (
 id uuid PRIMARY KEY,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
 schema_version integer NOT NULL DEFAULT 1,
 revision bigint NOT NULL DEFAULT 1,
 values_json jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(values_json) = 'object'),
 deleted boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS settings_profiles_owner ON settings_profiles(user_id);
CREATE TABLE IF NOT EXISTS settings_profile_receipts (
 profile_id uuid NOT NULL REFERENCES settings_profiles(id) ON DELETE CASCADE,
 mutation_id uuid NOT NULL,
 payload_hash text NOT NULL,
 PRIMARY KEY (profile_id, mutation_id)
);
