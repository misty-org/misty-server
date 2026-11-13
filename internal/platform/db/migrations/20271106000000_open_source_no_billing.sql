-- +goose Up
-- Misty has no billing. Every feature is open to every account and models run
-- on keys the person or the operator brings. This removes the billing adapter's
-- durable state, voice usage journals, cloud storage admission, referrals, plan
-- columns on the account record and the usage counters that only fed billing.

-- Billing adapter intents, outbox and reservations. Their worker and account
-- notification triggers go with them.
DROP TABLE IF EXISTS billing_adapter_intents;
DROP TABLE IF EXISTS billing_adapter_outbox;
DROP TABLE IF EXISTS billing_adapter_reservations;
DROP FUNCTION IF EXISTS misty_notify_billing_usage();

-- Realtime voice usage awaiting settlement.
DROP TABLE IF EXISTS voice_usage_journal;

-- Cloud storage growth checked against plan limits at commit. CASCADE drops
-- every cloud_bytes trigger that still calls the function.
DROP FUNCTION IF EXISTS record_account_cloud_change() CASCADE;
DROP TABLE IF EXISTS account_cloud_mutations;

-- Referral codes, claims and the signup signals that screened them.
DROP TABLE IF EXISTS referral_claims;
DROP TABLE IF EXISTS referral_codes;
DROP TABLE IF EXISTS account_signup_signals;

-- The license row stays as the account's identity record (users.license_id);
-- it no longer carries a plan.
ALTER TABLE licenses
    DROP COLUMN IF EXISTS tier,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS trial_started_at,
    DROP COLUMN IF EXISTS legacy_tier;

-- Coding leases keep their token and size; what each tool used was only ever
-- settled against a weekly allowance.
DROP INDEX IF EXISTS coding_leases_unsettled;
ALTER TABLE coding_leases
    DROP COLUMN IF EXISTS used_tokens,
    DROP COLUMN IF EXISTS input_tokens,
    DROP COLUMN IF EXISTS output_tokens,
    DROP COLUMN IF EXISTS cache_write_tokens,
    DROP COLUMN IF EXISTS cache_read_tokens,
    DROP COLUMN IF EXISTS settled_at;

-- Live transcription tickets no longer hold a reservation or settle their audio.
ALTER TABLE voice_live_sessions
    DROP COLUMN IF EXISTS used_ms,
    DROP COLUMN IF EXISTS reservation,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS settled_at;

-- Goals no longer pause at a usage limit and resume when it refills.
DROP TRIGGER IF EXISTS agent_goals_resume_worker_notify ON agent_goals;
DROP INDEX IF EXISTS agent_goals_resume_due;
ALTER TABLE agent_goals DROP COLUMN IF EXISTS resume_at;

-- A Space run names the account whose models run it (the member who asked),
-- not who pays for it.
ALTER TABLE space_runs RENAME COLUMN billing_user_id TO model_user_id;
ALTER TABLE space_runs RENAME CONSTRAINT space_runs_billing_user_id_fkey TO space_runs_model_user_id_fkey;
ALTER TABLE library_processing_jobs DROP COLUMN IF EXISTS billing_user_id;

-- +goose Down
ALTER TABLE agent_goals ADD COLUMN resume_at timestamptz;
CREATE INDEX agent_goals_resume_due ON agent_goals(resume_at) WHERE status = 'paused' AND resume_at IS NOT NULL;
CREATE TRIGGER agent_goals_resume_worker_notify AFTER UPDATE OF resume_at ON agent_goals
    FOR EACH ROW WHEN (NEW.resume_at IS NOT NULL) EXECUTE FUNCTION misty_notify_worker_queue('goal-resume');
ALTER TABLE library_processing_jobs ADD COLUMN billing_user_id text REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE space_runs RENAME CONSTRAINT space_runs_model_user_id_fkey TO space_runs_billing_user_id_fkey;
ALTER TABLE space_runs RENAME COLUMN model_user_id TO billing_user_id;

ALTER TABLE voice_live_sessions
    ADD COLUMN used_ms bigint NOT NULL DEFAULT 0 CHECK (used_ms >= 0),
    ADD COLUMN reservation jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'settled', 'released')),
    ADD COLUMN settled_at timestamptz;

ALTER TABLE coding_leases
    ADD COLUMN used_tokens bigint NOT NULL DEFAULT 0 CHECK (used_tokens >= 0),
    ADD COLUMN input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    ADD COLUMN output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    ADD COLUMN cache_write_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    ADD COLUMN cache_read_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    ADD COLUMN settled_at timestamptz;
CREATE INDEX coding_leases_unsettled ON coding_leases(invocation_id) WHERE settled_at IS NULL;

ALTER TABLE licenses
    ADD COLUMN tier text NOT NULL DEFAULT 'free',
    ADD COLUMN status text NOT NULL DEFAULT 'active',
    ADD COLUMN expires_at timestamptz,
    ADD COLUMN trial_started_at timestamptz,
    ADD COLUMN legacy_tier text;

-- Dropped billing, voice usage, referral and cloud admission records are not
-- restored; their tables come back empty.
CREATE TABLE account_signup_signals (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    network_hash text NOT NULL DEFAULT '',
    device_hash text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE referral_codes (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE referral_claims (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    referrer_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    same_network boolean NOT NULL,
    same_device boolean NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    CHECK (user_id <> referrer_id)
);
ALTER TABLE account_signup_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_signup_signals FORCE ROW LEVEL SECURITY;
CREATE POLICY account_signup_signals_service ON account_signup_signals
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
ALTER TABLE referral_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE referral_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY referral_codes_owner ON referral_codes
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
ALTER TABLE referral_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE referral_claims FORCE ROW LEVEL SECURITY;
CREATE POLICY referral_claims_service ON referral_claims
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE TABLE account_cloud_mutations (
    transaction_id bigint NOT NULL,
    user_id text NOT NULL,
    added_bytes bigint NOT NULL DEFAULT 0,
    removed_bytes bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (transaction_id, user_id)
);
CREATE TABLE voice_usage_journal (
    operation_id text PRIMARY KEY,
    account_id text NOT NULL,
    reservation_id text NOT NULL,
    state text NOT NULL,
    usage jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE billing_adapter_reservations (
    account_id text NOT NULL,
    key text NOT NULL,
    reservation_id text NOT NULL,
    admission jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, key)
);
CREATE TABLE billing_adapter_outbox (
    id text PRIMARY KEY,
    action text NOT NULL,
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    rejected_at timestamptz
);
CREATE TABLE billing_adapter_intents (
    account_id text NOT NULL,
    key text NOT NULL,
    admission jsonb NOT NULL,
    state text NOT NULL DEFAULT 'pending',
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '2 minutes'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, key)
);
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON account_signup_signals, referral_codes, referral_claims,
            account_cloud_mutations, voice_usage_journal, billing_adapter_reservations,
            billing_adapter_outbox, billing_adapter_intents TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
