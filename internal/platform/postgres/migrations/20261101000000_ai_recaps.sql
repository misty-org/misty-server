-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE ai_recaps (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface_id TEXT NOT NULL CHECK(char_length(surface_id) BETWEEN 1 AND 80),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    cadence TEXT NOT NULL DEFAULT 'daily' CHECK(cadence IN ('daily','weekly')),
    local_time TEXT NOT NULL DEFAULT '08:00' CHECK(local_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    weekday SMALLINT NOT NULL DEFAULT 1 CHECK(weekday BETWEEN 0 AND 6),
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK(char_length(timezone) BETWEEN 1 AND 100),
    prompt TEXT NOT NULL DEFAULT '' CHECK(char_length(prompt)<=8000),
    state TEXT NOT NULL DEFAULT 'idle' CHECK(state IN ('idle','running','failed')),
    next_run_at TIMESTAMPTZ,
    lease_until TIMESTAMPTZ,
    last_invocation_id TEXT REFERENCES ai_invocations(id) ON DELETE SET NULL,
    last_result TEXT NOT NULL DEFAULT '' CHECK(char_length(last_result)<=100000),
    last_citations JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(last_citations)='array'),
    last_error TEXT NOT NULL DEFAULT '' CHECK(char_length(last_error)<=2000),
    last_run_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,surface_id)
);
CREATE INDEX ai_recaps_due_idx ON ai_recaps(next_run_at) WHERE enabled;

ALTER TABLE ai_recaps ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_recaps FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_recaps_owner_policy ON ai_recaps FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON ai_recaps TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS ai_recaps;
