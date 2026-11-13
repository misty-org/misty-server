-- +goose Up
-- Web search runs on the person's desktop on a search key they keep there,
-- so the server counts and limits no searches.
DELETE FROM account_usage WHERE metric = 'web_searches';
ALTER TABLE account_usage DROP CONSTRAINT IF EXISTS account_usage_metric_check;
ALTER TABLE account_usage ADD CONSTRAINT account_usage_metric_check CHECK (metric IN ('app_actions'));
ALTER TABLE account_controls DROP COLUMN IF EXISTS searches_per_month;

-- +goose Down
ALTER TABLE account_controls
  ADD COLUMN searches_per_month bigint CHECK (searches_per_month IS NULL OR searches_per_month >= 0);
ALTER TABLE account_usage DROP CONSTRAINT IF EXISTS account_usage_metric_check;
ALTER TABLE account_usage ADD CONSTRAINT account_usage_metric_check CHECK (metric IN ('web_searches', 'app_actions'));
