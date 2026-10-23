-- +goose Up
-- One row per live transcription ticket (docs/voice-memory/README.md).
-- The ticket's id is single-use: the runtime settles it once, with the audio it
-- actually streamed, against the billing reservation taken when it was issued.
-- Weekly voice minutes are summed from these rows.
CREATE TABLE voice_live_sessions (
    id uuid PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id text NOT NULL CHECK (char_length(device_id) BETWEEN 1 AND 200),
    channels text[] NOT NULL CHECK (cardinality(channels) BETWEEN 1 AND 2 AND channels <@ ARRAY['mic','system']),
    max_ms bigint NOT NULL CHECK (max_ms > 0),
    used_ms bigint NOT NULL DEFAULT 0 CHECK (used_ms >= 0),
    reservation jsonb NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'settled', 'released')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    settled_at timestamptz
);
CREATE INDEX voice_live_sessions_owner_created ON voice_live_sessions (owner_user_id, created_at DESC);
ALTER TABLE voice_live_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE voice_live_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY voice_live_sessions_owner ON voice_live_sessions
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE ON voice_live_sessions TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS voice_live_sessions;
