-- +goose Up
-- Two more per-account limits, each unlimited unless set: how many of the Spaces an account
-- owns may be shared with other people, and how many people one of its Spaces may hold. The
-- billing service decides the numbers; this server only counts.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE account_controls
  ADD COLUMN shared_spaces bigint CHECK (shared_spaces IS NULL OR shared_spaces >= 0),
  ADD COLUMN space_members bigint CHECK (space_members IS NULL OR space_members >= 0);

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE account_controls
  DROP COLUMN shared_spaces,
  DROP COLUMN space_members;
