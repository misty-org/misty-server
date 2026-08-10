-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_rls_mode()
RETURNS TEXT
LANGUAGE SQL
STABLE
AS $$
    SELECT NULLIF(current_setting('app.rls_mode', true), '')
$$;

CREATE OR REPLACE FUNCTION misty_rls_user_id()
RETURNS TEXT
LANGUAGE SQL
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_user_id', true), '')
$$;

CREATE OR REPLACE FUNCTION misty_rls_email()
RETURNS TEXT
LANGUAGE SQL
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_email', true), '')
$$;

CREATE OR REPLACE FUNCTION misty_rls_license_id()
RETURNS TEXT
LANGUAGE SQL
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_license_id', true), '')
$$;

CREATE OR REPLACE FUNCTION misty_rls_session_token_hash()
RETURNS TEXT
LANGUAGE SQL
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_session_token_hash', true), '')
$$;

CREATE OR REPLACE FUNCTION misty_rls_is_service()
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
AS $$
    SELECT misty_rls_mode() = 'service'
$$;

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS users_select_policy ON users;
DROP POLICY IF EXISTS users_insert_policy ON users;
DROP POLICY IF EXISTS users_update_policy ON users;
DROP POLICY IF EXISTS users_delete_policy ON users;
CREATE POLICY users_select_policy ON users
    FOR SELECT
    USING (
        misty_rls_is_service()
        OR id = misty_rls_user_id()
        OR (
            misty_rls_mode() = 'anonymous'
            AND LOWER(email) = misty_rls_email()
        )
    );
CREATE POLICY users_insert_policy ON users
    FOR INSERT
    WITH CHECK (
        misty_rls_mode() = 'registration'
        AND id = misty_rls_user_id()
        AND license_id = misty_rls_license_id()
        AND LOWER(email) = misty_rls_email()
    );
CREATE POLICY users_update_policy ON users
    FOR UPDATE
    USING (misty_rls_is_service() OR id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR id = misty_rls_user_id());
CREATE POLICY users_delete_policy ON users
    FOR DELETE
    USING (misty_rls_is_service());

ALTER TABLE licenses ENABLE ROW LEVEL SECURITY;
ALTER TABLE licenses FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS licenses_select_policy ON licenses;
DROP POLICY IF EXISTS licenses_insert_policy ON licenses;
DROP POLICY IF EXISTS licenses_update_policy ON licenses;
DROP POLICY IF EXISTS licenses_delete_policy ON licenses;
CREATE POLICY licenses_select_policy ON licenses
    FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY licenses_insert_policy ON licenses
    FOR INSERT
    WITH CHECK (
        misty_rls_is_service()
        OR (
            misty_rls_mode() = 'registration'
            AND user_id = misty_rls_user_id()
            AND id = misty_rls_license_id()
        )
    );
CREATE POLICY licenses_update_policy ON licenses
    FOR UPDATE
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY licenses_delete_policy ON licenses
    FOR DELETE
    USING (misty_rls_is_service());

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS sessions_select_policy ON sessions;
DROP POLICY IF EXISTS sessions_insert_policy ON sessions;
DROP POLICY IF EXISTS sessions_update_policy ON sessions;
DROP POLICY IF EXISTS sessions_delete_policy ON sessions;
CREATE POLICY sessions_select_policy ON sessions
    FOR SELECT
    USING (
        misty_rls_is_service()
        OR (
            misty_rls_mode() = 'session'
            AND token_hash = misty_rls_session_token_hash()
        )
    );
CREATE POLICY sessions_insert_policy ON sessions
    FOR INSERT
    WITH CHECK (
        misty_rls_is_service()
        OR (
            misty_rls_mode() = 'session'
            AND token_hash = misty_rls_session_token_hash()
            AND user_id = misty_rls_user_id()
        )
    );
CREATE POLICY sessions_update_policy ON sessions
    FOR UPDATE
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());
CREATE POLICY sessions_delete_policy ON sessions
    FOR DELETE
    USING (
        misty_rls_is_service()
        OR (
            misty_rls_mode() = 'session'
            AND token_hash = misty_rls_session_token_hash()
        )
    );

ALTER TABLE password_reset_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE password_reset_tokens FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS password_reset_tokens_all_policy ON password_reset_tokens;
CREATE POLICY password_reset_tokens_all_policy ON password_reset_tokens
    FOR ALL
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());

ALTER TABLE waitlist_signups ENABLE ROW LEVEL SECURITY;
ALTER TABLE waitlist_signups FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS waitlist_signups_select_policy ON waitlist_signups;
DROP POLICY IF EXISTS waitlist_signups_insert_policy ON waitlist_signups;
DROP POLICY IF EXISTS waitlist_signups_update_policy ON waitlist_signups;
DROP POLICY IF EXISTS waitlist_signups_delete_policy ON waitlist_signups;
CREATE POLICY waitlist_signups_select_policy ON waitlist_signups
    FOR SELECT
    USING (misty_rls_is_service());
CREATE POLICY waitlist_signups_insert_policy ON waitlist_signups
    FOR INSERT
    WITH CHECK (
        misty_rls_is_service()
        OR (
            misty_rls_mode() = 'waitlist'
            AND LOWER(email) = misty_rls_email()
        )
    );
CREATE POLICY waitlist_signups_update_policy ON waitlist_signups
    FOR UPDATE
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());
CREATE POLICY waitlist_signups_delete_policy ON waitlist_signups
    FOR DELETE
    USING (misty_rls_is_service());

ALTER TABLE stripe_purchases ENABLE ROW LEVEL SECURITY;
ALTER TABLE stripe_purchases FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS stripe_purchases_select_policy ON stripe_purchases;
DROP POLICY IF EXISTS stripe_purchases_insert_policy ON stripe_purchases;
DROP POLICY IF EXISTS stripe_purchases_update_policy ON stripe_purchases;
DROP POLICY IF EXISTS stripe_purchases_delete_policy ON stripe_purchases;
CREATE POLICY stripe_purchases_select_policy ON stripe_purchases
    FOR SELECT
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY stripe_purchases_insert_policy ON stripe_purchases
    FOR INSERT
    WITH CHECK (misty_rls_is_service());
CREATE POLICY stripe_purchases_update_policy ON stripe_purchases
    FOR UPDATE
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());
CREATE POLICY stripe_purchases_delete_policy ON stripe_purchases
    FOR DELETE
    USING (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE stripe_purchases DISABLE ROW LEVEL SECURITY;
ALTER TABLE stripe_purchases NO FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS stripe_purchases_select_policy ON stripe_purchases;
DROP POLICY IF EXISTS stripe_purchases_insert_policy ON stripe_purchases;
DROP POLICY IF EXISTS stripe_purchases_update_policy ON stripe_purchases;
DROP POLICY IF EXISTS stripe_purchases_delete_policy ON stripe_purchases;

ALTER TABLE waitlist_signups DISABLE ROW LEVEL SECURITY;
ALTER TABLE waitlist_signups NO FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS waitlist_signups_select_policy ON waitlist_signups;
DROP POLICY IF EXISTS waitlist_signups_insert_policy ON waitlist_signups;
DROP POLICY IF EXISTS waitlist_signups_update_policy ON waitlist_signups;
DROP POLICY IF EXISTS waitlist_signups_delete_policy ON waitlist_signups;

ALTER TABLE password_reset_tokens DISABLE ROW LEVEL SECURITY;
ALTER TABLE password_reset_tokens NO FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS password_reset_tokens_all_policy ON password_reset_tokens;

ALTER TABLE sessions DISABLE ROW LEVEL SECURITY;
ALTER TABLE sessions NO FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS sessions_select_policy ON sessions;
DROP POLICY IF EXISTS sessions_insert_policy ON sessions;
DROP POLICY IF EXISTS sessions_update_policy ON sessions;
DROP POLICY IF EXISTS sessions_delete_policy ON sessions;

ALTER TABLE licenses DISABLE ROW LEVEL SECURITY;
ALTER TABLE licenses NO FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS licenses_select_policy ON licenses;
DROP POLICY IF EXISTS licenses_insert_policy ON licenses;
DROP POLICY IF EXISTS licenses_update_policy ON licenses;
DROP POLICY IF EXISTS licenses_delete_policy ON licenses;

ALTER TABLE users DISABLE ROW LEVEL SECURITY;
ALTER TABLE users NO FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS users_select_policy ON users;
DROP POLICY IF EXISTS users_insert_policy ON users;
DROP POLICY IF EXISTS users_update_policy ON users;
DROP POLICY IF EXISTS users_delete_policy ON users;

DROP FUNCTION IF EXISTS misty_rls_is_service();
DROP FUNCTION IF EXISTS misty_rls_session_token_hash();
DROP FUNCTION IF EXISTS misty_rls_license_id();
DROP FUNCTION IF EXISTS misty_rls_email();
DROP FUNCTION IF EXISTS misty_rls_user_id();
DROP FUNCTION IF EXISTS misty_rls_mode();
-- +goose StatementEnd
