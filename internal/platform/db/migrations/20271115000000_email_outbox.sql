-- +goose Up
-- A server with Mailjet configured sends email. Each
-- email is stored before it is sent and retried until Mailjet takes it:
--   setup             the sign-in link of a new account
--   sign_in           a sign-in link for an account that has signed in before
--   closed            the notice when a closed account becomes read-only
--   deletion_warning  the reminder 3 days before that account is deleted
--   invitation        a Space invitation's link
-- A row holds its key or link only while it is pending: sending it, a newer one,
-- or its expiry clear them. Without Mailjet nothing is stored here at all.
CREATE TABLE email_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL CHECK (kind IN ('setup', 'sign_in', 'closed', 'deletion_warning', 'invitation')),
  user_id text REFERENCES users(id) ON DELETE CASCADE,
  invitation_id text,
  recipient text NOT NULL,
  setup_key text,
  link text,
  space_name text,
  inviter_name text,
  delete_at timestamptz,
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'cancelled')),
  cancel_reason text,
  send_after timestamptz NOT NULL DEFAULT now(),
  attempts integer NOT NULL DEFAULT 0,
  last_error text,
  sent_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind IN ('closed', 'deletion_warning')) = (delete_at IS NOT NULL)),
  CHECK ((kind = 'invitation') = (invitation_id IS NOT NULL)),
  CHECK (kind = 'invitation' OR user_id IS NOT NULL),
  CHECK (kind IN ('setup', 'sign_in') OR setup_key IS NULL),
  CHECK (state = 'pending' OR (setup_key IS NULL AND (link IS NULL OR kind IN ('closed', 'deletion_warning'))))
);
CREATE UNIQUE INDEX email_outbox_closing ON email_outbox(user_id, kind, delete_at)
  WHERE kind IN ('closed', 'deletion_warning');
CREATE INDEX email_outbox_user ON email_outbox(user_id);
CREATE INDEX email_outbox_invitation ON email_outbox(invitation_id) WHERE invitation_id IS NOT NULL;
CREATE INDEX email_outbox_due ON email_outbox(send_after) WHERE state = 'pending';
ALTER TABLE email_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY email_outbox_service ON email_outbox
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose Down
DROP TABLE IF EXISTS email_outbox;
