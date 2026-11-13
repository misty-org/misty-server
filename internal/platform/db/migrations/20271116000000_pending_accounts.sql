-- +goose Up
-- On a server that sends email, a new password account waits here until its email is
-- confirmed, so nobody can hold an address that isn't theirs. It has no users row, no
-- session and no data until then. The newest sign-up for an email replaces the last.
CREATE TABLE pending_accounts (
  email text PRIMARY KEY,
  username text NOT NULL,
  password_hash text NOT NULL,
  credential_password boolean NOT NULL,
  token_hash text NOT NULL UNIQUE,
  sent_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX pending_accounts_expiry ON pending_accounts(expires_at);
ALTER TABLE pending_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE pending_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY pending_accounts_service ON pending_accounts
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- confirm_email goes to an address with no account yet, so it has no user.
ALTER TABLE email_outbox
  DROP CONSTRAINT email_outbox_kind_check,
  DROP CONSTRAINT email_outbox_check2,
  ADD CONSTRAINT email_outbox_kind_check CHECK (kind IN ('setup', 'sign_in', 'closed', 'deletion_warning', 'invitation', 'confirm_email')),
  ADD CONSTRAINT email_outbox_check2 CHECK (kind IN ('invitation', 'confirm_email') OR user_id IS NOT NULL);

-- +goose Down
DELETE FROM email_outbox WHERE kind = 'confirm_email';
ALTER TABLE email_outbox
  DROP CONSTRAINT email_outbox_kind_check,
  DROP CONSTRAINT email_outbox_check2,
  ADD CONSTRAINT email_outbox_kind_check CHECK (kind IN ('setup', 'sign_in', 'closed', 'deletion_warning', 'invitation')),
  ADD CONSTRAINT email_outbox_check2 CHECK (kind = 'invitation' OR user_id IS NOT NULL);
DROP TABLE IF EXISTS pending_accounts;
