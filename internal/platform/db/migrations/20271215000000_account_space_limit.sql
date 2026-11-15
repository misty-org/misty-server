-- +goose Up
-- How many active Spaces an account may belong to, its own and the ones it joined together.
-- NULL is unlimited, every account's default; Misty Cloud's plans set it.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE account_controls
  ADD COLUMN spaces bigint CHECK (spaces IS NULL OR spaces >= 0);

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE account_controls
  DROP COLUMN spaces;
