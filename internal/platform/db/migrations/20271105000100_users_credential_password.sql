-- +goose Up
-- Whether the stored password hash is of a credential the desktop app derived on
-- the device (true), or of the password as typed (false: accounts made before
-- derived credentials, and any password reset by email). The sync vault key is
-- derived from the password, so only an account whose server never saw it in
-- plain form can seal a vault under it. Clients use this to correct their own
-- record; it is not a secret.
ALTER TABLE public.users ADD COLUMN credential_password boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE public.users DROP COLUMN credential_password;
