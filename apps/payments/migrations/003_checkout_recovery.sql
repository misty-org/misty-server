-- +goose Up
ALTER TABLE billing.checkout_attempts
  ADD COLUMN recovery_cursor TEXT,
  ADD COLUMN recovery_after TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN recovery_failures INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN last_recovery_error TEXT;
CREATE INDEX checkout_attempts_recovery_due ON billing.checkout_attempts(recovery_after,expires_at)
  WHERE status='creating';
