-- +goose Up
-- A vault belongs to a person and a Space: one per person per Space. The
-- personal Space adopts the vault the account already has, so its vault_id is
-- unchanged. Only the personal vault keeps the password-wrapped key_envelope;
-- every other vault's root is sealed to the personal root (space_key_envelope),
-- so one sync password opens them all. The server sees only sealed bytes.
-- browser_sync_* tables carry no RLS policies (the sync store checks the
-- account on every query), and the cloud_bytes trigger already finds a vault's
-- person by vault_id, so every Space vault counts toward its person's storage.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    fks text[][] := ARRAY[]::text[][];
    fk text[];
BEGIN
    ALTER TABLE public.browser_sync_vaults ADD COLUMN space_id text;
    UPDATE public.browser_sync_vaults SET space_id = misty_ensure_personal_space(user_id);
    -- An account that is no longer active has no personal Space and no use for its vault.
    DELETE FROM public.browser_sync_vaults WHERE space_id IS NULL;
    ALTER TABLE public.browser_sync_vaults
        ALTER COLUMN space_id SET NOT NULL,
        ADD CONSTRAINT browser_sync_vaults_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE,
        ADD CONSTRAINT browser_sync_vaults_user_id_space_id_key UNIQUE (user_id, space_id),
        ALTER COLUMN key_envelope DROP NOT NULL,
        ADD COLUMN space_key_envelope jsonb,
        ADD CONSTRAINT browser_sync_vaults_space_key_envelope_check CHECK (jsonb_typeof(space_key_envelope) = 'object'),
        ADD CONSTRAINT browser_sync_vaults_one_envelope CHECK ((key_envelope IS NULL) <> (space_key_envelope IS NULL));
    -- One password-wrapped (personal) vault per account.
    CREATE UNIQUE INDEX browser_sync_vaults_personal_key ON public.browser_sync_vaults (user_id) WHERE key_envelope IS NOT NULL;

    -- vault_id becomes the primary key. Foreign keys bind to the unique index
    -- that is being replaced, so they are dropped and recreated around the swap.
    FOR r IN
        SELECT t.relname::text AS table_name, c.conname::text AS name, pg_get_constraintdef(c.oid) AS def
        FROM pg_constraint c
        JOIN pg_class t ON t.oid = c.conrelid
        WHERE c.contype = 'f' AND c.confrelid = 'public.browser_sync_vaults'::regclass
    LOOP
        fks := fks || ARRAY[ARRAY[r.table_name, r.name, r.def]];
        EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT %I', r.table_name, r.name);
    END LOOP;
    ALTER TABLE public.browser_sync_vaults DROP CONSTRAINT browser_sync_vaults_pkey;
    ALTER TABLE public.browser_sync_vaults DROP CONSTRAINT browser_sync_vaults_vault_id_key;
    ALTER TABLE public.browser_sync_vaults ADD CONSTRAINT browser_sync_vaults_pkey PRIMARY KEY (vault_id);
    IF array_length(fks, 1) IS NOT NULL THEN
        FOREACH fk SLICE 1 IN ARRAY fks
        LOOP
            EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I %s', fk[1], fk[2], fk[3]);
        END LOOP;
    END IF;
    CREATE INDEX browser_sync_vaults_user_id_idx ON public.browser_sync_vaults (user_id);
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    fks text[][] := ARRAY[]::text[][];
    fk text[];
BEGIN
    DELETE FROM public.browser_sync_vaults WHERE space_key_envelope IS NOT NULL;
    FOR r IN
        SELECT t.relname::text AS table_name, c.conname::text AS name, pg_get_constraintdef(c.oid) AS def
        FROM pg_constraint c
        JOIN pg_class t ON t.oid = c.conrelid
        WHERE c.contype = 'f' AND c.confrelid = 'public.browser_sync_vaults'::regclass
    LOOP
        fks := fks || ARRAY[ARRAY[r.table_name, r.name, r.def]];
        EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT %I', r.table_name, r.name);
    END LOOP;
    DROP INDEX public.browser_sync_vaults_user_id_idx;
    DROP INDEX public.browser_sync_vaults_personal_key;
    ALTER TABLE public.browser_sync_vaults DROP CONSTRAINT browser_sync_vaults_pkey;
    ALTER TABLE public.browser_sync_vaults
        DROP CONSTRAINT browser_sync_vaults_one_envelope,
        DROP CONSTRAINT browser_sync_vaults_space_key_envelope_check,
        DROP CONSTRAINT browser_sync_vaults_user_id_space_id_key,
        DROP CONSTRAINT browser_sync_vaults_space_id_fkey,
        DROP COLUMN space_key_envelope,
        DROP COLUMN space_id,
        ALTER COLUMN key_envelope SET NOT NULL,
        ADD CONSTRAINT browser_sync_vaults_pkey PRIMARY KEY (user_id),
        ADD CONSTRAINT browser_sync_vaults_vault_id_key UNIQUE (vault_id);
    IF array_length(fks, 1) IS NOT NULL THEN
        FOREACH fk SLICE 1 IN ARRAY fks
        LOOP
            EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I %s', fk[1], fk[2], fk[3]);
        END LOOP;
    END IF;
END
$$;
-- +goose StatementEnd
