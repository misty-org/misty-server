-- +goose Up
-- +goose StatementBegin
-- Short-lived, single-use tokens that let the desktop app open the website
-- already signed in. Unlike password_reset_tokens these are keyed by the token
-- hash rather than the user, so a user may have several in flight, and they are
-- consumed on first read rather than on a terminal action.
CREATE TABLE auth_handoff_tokens (
    hashed_token VARCHAR(64) PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_path TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(hashed_token) = 64)
);

CREATE INDEX idx_auth_handoff_tokens_expires_at
    ON auth_handoff_tokens (expires_at);

ALTER TABLE auth_handoff_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_handoff_tokens FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS auth_handoff_tokens_all_policy ON auth_handoff_tokens;
CREATE POLICY auth_handoff_tokens_all_policy ON auth_handoff_tokens
    FOR ALL
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS auth_handoff_tokens;
-- +goose StatementEnd
