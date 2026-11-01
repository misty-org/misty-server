-- +goose Up
-- Closing an account (docs/cloud/README.md). The operator, or billing, closes an
-- account: it is suspended at once and its data is deleted on delete_at with
-- the same full purge as a person deleting their own account. A notice is
-- emailed when it closes and a warning three days before deletion. Resuming
-- clears all of it. The worker sends the emails, so a failed send is retried.
--
-- Monthly web searches and app actions are counted per billing period. With a
-- period_anchor the period starts on the anchor's day each month (the 29th to
-- 31st fall on a shorter month's last day); without one it is the calendar
-- month. Times are UTC.
ALTER TABLE account_controls
  ADD COLUMN period_anchor timestamptz,
  ADD COLUMN delete_at timestamptz,
  ADD COLUMN close_notice_sent_at timestamptz,
  ADD COLUMN deletion_warning_sent_at timestamptz,
  ADD CONSTRAINT account_controls_closing_suspended CHECK (delete_at IS NULL OR suspended_at IS NOT NULL);
CREATE INDEX account_controls_delete_at_idx ON account_controls(delete_at) WHERE delete_at IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION misty_usage_period(anchor timestamptz, at timestamptz) RETURNS date
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  WITH t AS (
    SELECT (at AT TIME ZONE 'UTC')::date AS today,
           date_trunc('month', at AT TIME ZONE 'UTC')::date AS month,
           extract(day FROM anchor AT TIME ZONE 'UTC')::int AS day
  ), candidate AS (
    SELECT today, month, day,
           LEAST(month + (day - 1), (month + interval '1 month' - interval '1 day')::date) AS this_month
    FROM t
  )
  SELECT CASE
    WHEN anchor IS NULL THEN month
    WHEN this_month <= today THEN this_month
    ELSE LEAST((month - interval '1 month')::date + (day - 1), month - 1)
  END FROM candidate
$$;
-- +goose StatementEnd

-- Closing an account wakes the account-deletion worker.
CREATE TRIGGER account_closing_notify AFTER INSERT OR UPDATE OF delete_at ON account_controls
FOR EACH ROW WHEN (NEW.delete_at IS NOT NULL)
EXECUTE FUNCTION misty_notify_worker_queue('account-deletion');

-- +goose Down
DROP TRIGGER IF EXISTS account_closing_notify ON account_controls;
DROP FUNCTION IF EXISTS misty_usage_period(timestamptz, timestamptz);
DROP INDEX IF EXISTS account_controls_delete_at_idx;
ALTER TABLE account_controls
  DROP CONSTRAINT IF EXISTS account_controls_closing_suspended,
  DROP COLUMN IF EXISTS deletion_warning_sent_at,
  DROP COLUMN IF EXISTS close_notice_sent_at,
  DROP COLUMN IF EXISTS delete_at,
  DROP COLUMN IF EXISTS period_anchor;
