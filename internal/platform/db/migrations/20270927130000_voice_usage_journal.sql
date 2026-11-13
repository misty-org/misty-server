-- +goose Up
-- Server-only accounting metadata. Never stores audio, screenshots or transcripts.
CREATE TABLE voice_usage_journal (
    operation_id text PRIMARY KEY,
    account_id text NOT NULL,
    reservation_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('active','reconcile','settlement_pending','closed')),
    usage jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX voice_usage_journal_pending ON voice_usage_journal(state, updated_at)
WHERE state <> 'closed';

-- +goose Down
DROP TABLE voice_usage_journal;
