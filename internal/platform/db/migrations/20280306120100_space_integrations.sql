-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS space_integrations CASCADE;

CREATE TABLE space_integrations (
  id text PRIMARY KEY CHECK (id ~ '^int_[a-z2-7]{26}$'),
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
  avatar jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(avatar) = 'object'),
  created_by text NOT NULL REFERENCES users(id),
  access text[] NOT NULL CHECK (cardinality(access) BETWEEN 1 AND 2
    AND access <@ ARRAY['rooms.read', 'rooms.write']::text[]),
  key_prefix text NOT NULL DEFAULT '' CHECK (octet_length(key_prefix) <= 256),
  token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz
);
CREATE INDEX space_integrations_space_idx ON space_integrations (space_id, created_at);
ALTER TABLE space_integrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_integrations_service ON space_integrations
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE account_controls
  ADD COLUMN integrations_per_space bigint CHECK (integrations_per_space IS NULL OR integrations_per_space >= 0);

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_integrations TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE account_controls DROP COLUMN IF EXISTS integrations_per_space;
DROP TABLE IF EXISTS space_integrations;

CREATE TABLE public.space_integrations (
    id text NOT NULL PRIMARY KEY,
    space_id text NOT NULL REFERENCES public.spaces(id) ON DELETE CASCADE,
    provider text NOT NULL,
    display_name text NOT NULL,
    credential_reference text NOT NULL,
    granted_permissions jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    connected_by_user_id text NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_integrations_granted_permissions_check CHECK ((jsonb_typeof(granted_permissions) = 'array'::text)),
    CONSTRAINT space_integrations_status_check CHECK ((status = ANY (ARRAY['active'::text, 'needs_attention'::text, 'disabled'::text]))),
    CONSTRAINT space_integrations_private_account_key UNIQUE (space_id, connected_by_user_id, provider, display_name)
);
CREATE INDEX space_integrations_space_idx ON public.space_integrations USING btree (space_id, provider);
ALTER TABLE public.space_integrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ONLY public.space_integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_integrations_policy ON public.space_integrations
  USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)))
  WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));
