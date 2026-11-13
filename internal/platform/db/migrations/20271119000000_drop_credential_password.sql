-- +goose Up
-- The app sends the password as typed and the server hashes it, so nothing derives a login
-- credential any more. A sign-up still waiting holds a hash of a derived credential that no
-- one can type, so it has to sign up again.
DELETE FROM pending_accounts WHERE credential_password;
ALTER TABLE pending_accounts DROP COLUMN credential_password;
ALTER TABLE public.users DROP COLUMN credential_password;

-- +goose Down
ALTER TABLE public.users ADD COLUMN credential_password boolean NOT NULL DEFAULT false;
ALTER TABLE pending_accounts ADD COLUMN credential_password boolean NOT NULL DEFAULT false;
