-- +goose Up
-- Diarized transcripts from voice captures and transcribed files
-- (docs/voice-memory/README.md). Text is kept until deleted; audio is
-- kept only when the person chose "Keep recording", as a Library item.
CREATE TABLE voice_transcripts (
    id uuid PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 200),
    source jsonb NOT NULL CHECK (jsonb_typeof(source) = 'object'),
    language text NOT NULL DEFAULT '' CHECK (char_length(language) <= 16),
    turns jsonb NOT NULL CHECK (jsonb_typeof(turns) = 'array'),
    speakers jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(speakers) = 'object'),
    duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    note_id text NOT NULL DEFAULT '',
    recording_item_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX voice_transcripts_owner_created ON voice_transcripts (owner_user_id, created_at DESC);
ALTER TABLE voice_transcripts ENABLE ROW LEVEL SECURITY;
ALTER TABLE voice_transcripts FORCE ROW LEVEL SECURITY;
CREATE POLICY voice_transcripts_owner ON voice_transcripts
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON voice_transcripts TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS voice_transcripts;
