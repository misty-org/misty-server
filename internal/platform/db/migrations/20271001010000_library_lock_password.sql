-- +goose Up
CREATE TABLE library_lock_credentials (
 user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 password_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT NOW()
);
ALTER TABLE library_lock_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE library_lock_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY library_lock_credentials_owner ON library_lock_credentials
 USING (public.misty_rls_is_service() OR user_id=public.misty_rls_user_id())
 WITH CHECK (public.misty_rls_is_service() OR user_id=public.misty_rls_user_id());

-- These grants were issued using the account sign-in password. Require the
-- separate library password on the first unlock after this migration.
DELETE FROM library_reauthentication_grants;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Library lock credentials are forward-only; restore a verified backup to roll back'; END $$;
-- +goose StatementEnd
