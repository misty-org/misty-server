-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_invitations ADD COLUMN by_account boolean NOT NULL DEFAULT false;

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_invitations DROP COLUMN IF EXISTS by_account;
