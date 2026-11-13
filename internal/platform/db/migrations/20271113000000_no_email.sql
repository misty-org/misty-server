-- +goose Up
-- The open server sends no email. The alerts service
-- records the problems it sees here instead of emailing them; the console and
-- the server owner's app read them. A row is an open alert: it is written once
-- a problem is seen on two checks in a row and deleted when it clears.
CREATE TABLE server_alerts (
  probe text PRIMARY KEY,
  title text NOT NULL,
  detail text NOT NULL,
  opened_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE server_alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_alerts FORCE ROW LEVEL SECURITY;
CREATE POLICY server_alerts_service ON server_alerts
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- A closing account is deleted on its date without waiting for emails.
ALTER TABLE account_controls
  DROP COLUMN IF EXISTS close_notice_sent_at,
  DROP COLUMN IF EXISTS deletion_warning_sent_at;

-- +goose Down
ALTER TABLE account_controls
  ADD COLUMN close_notice_sent_at timestamptz,
  ADD COLUMN deletion_warning_sent_at timestamptz;
DROP TABLE IF EXISTS server_alerts;
