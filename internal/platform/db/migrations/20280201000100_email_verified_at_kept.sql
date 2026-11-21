-- +goose Up
-- email_verified_at is kept from now on: set when a confirmation link, Google or the operator
-- proves an account's email, and left empty for a password account made without confirmation,
-- whose email an operator may release for its real owner. Accounts made before this were never
-- marked; they are taken as confirmed, as they were when the column was added.
SELECT set_config('app.rls_mode', 'service', true);

UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

-- +goose Down
SELECT 1;
