-- +goose Up
-- A closing account is read-only instead of suspended:
-- until delete_at it signs in, reads and exports, and changes nothing. Closing
-- no longer sets suspended_at, so the two are independent, and when both are
-- set the suspension wins. An account closed before this keeps the suspension
-- it was given; resume it and close it again to make it read-only.
ALTER TABLE account_controls DROP CONSTRAINT IF EXISTS account_controls_closing_suspended;

-- +goose Down
UPDATE account_controls SET suspended_at = COALESCE(suspended_at, now()) WHERE delete_at IS NOT NULL;
ALTER TABLE account_controls
  ADD CONSTRAINT account_controls_closing_suspended CHECK (delete_at IS NULL OR suspended_at IS NOT NULL);
