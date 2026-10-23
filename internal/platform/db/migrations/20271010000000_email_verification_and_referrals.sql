-- +goose Up
-- AI needs a verified email, so throwaway signups cannot farm the free plan.
-- Accounts that exist before this change are treated as verified: Google
-- accounts always were, and earlier password accounts keep working.
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;
UPDATE users SET email_verified_at = created_at;

CREATE TABLE email_verification_tokens (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    hashed_token varchar(64) NOT NULL UNIQUE CHECK (char_length(hashed_token) = 64),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE email_verification_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_verification_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY email_verification_tokens_service ON email_verification_tokens
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

-- Each account's shareable referral code.
CREATE TABLE referral_codes (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code text NOT NULL UNIQUE CHECK (code ~ '^[a-z0-9]{8}$'),
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE referral_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE referral_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY referral_codes_owner ON referral_codes
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- Hashed network and device seen when an account signed up, so a referral
-- from the referrer's own network or device can be refused. Raw addresses are
-- never stored.
CREATE TABLE account_signup_signals (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    network_hash text NOT NULL DEFAULT '',
    device_hash text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE account_signup_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_signup_signals FORCE ROW LEVEL SECURITY;
CREATE POLICY account_signup_signals_service ON account_signup_signals
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

-- A new account's claim of the link it joined through. Billing receives the
-- attribution once the email is verified; it decides whether it ever counts.
CREATE TABLE referral_claims (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    referrer_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    same_network boolean NOT NULL,
    same_device boolean NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    CHECK (user_id <> referrer_id)
);
ALTER TABLE referral_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE referral_claims FORCE ROW LEVEL SECURITY;
CREATE POLICY referral_claims_service ON referral_claims
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

-- A goal paused at a usage limit resumes by itself when the limit refills.
ALTER TABLE agent_goals ADD COLUMN resume_at timestamptz;
CREATE INDEX agent_goals_resume_due ON agent_goals(resume_at) WHERE status = 'paused' AND resume_at IS NOT NULL;
CREATE TRIGGER agent_goals_resume_worker_notify AFTER UPDATE OF resume_at ON agent_goals
    FOR EACH ROW WHEN (NEW.resume_at IS NOT NULL) EXECUTE FUNCTION misty_notify_worker_queue('goal-resume');

-- Billing deliveries that billing rejects as invalid or conflicting stop
-- retrying and wait for an operator instead of retrying every minute forever.
ALTER TABLE billing_adapter_outbox ADD COLUMN rejected_at timestamptz;
ALTER TABLE billing_adapter_outbox ADD COLUMN rejection text;

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON email_verification_tokens, referral_codes, account_signup_signals, referral_claims TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS agent_goals_resume_worker_notify ON agent_goals;
DROP INDEX IF EXISTS agent_goals_resume_due;
ALTER TABLE agent_goals DROP COLUMN IF EXISTS resume_at;
ALTER TABLE billing_adapter_outbox DROP COLUMN IF EXISTS rejection;
ALTER TABLE billing_adapter_outbox DROP COLUMN IF EXISTS rejected_at;
DROP TABLE IF EXISTS referral_claims;
DROP TABLE IF EXISTS account_signup_signals;
DROP TABLE IF EXISTS referral_codes;
DROP TABLE IF EXISTS email_verification_tokens;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
