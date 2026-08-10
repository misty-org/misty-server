-- +goose Up
-- +goose StatementBegin
SET CONSTRAINTS ALL DEFERRED;

DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
DROP FUNCTION IF EXISTS misty_provision_default_space_for_new_user();
DROP FUNCTION IF EXISTS misty_ensure_default_space(TEXT);

-- The per-account Misty Spaces were a short-lived local beta implementation.
-- Product direction now uses one canonical Space with isolated conversations.
DELETE FROM spaces WHERE kind='misty';
DELETE FROM security_domains WHERE id LIKE 'sd_misty_%';
SET CONSTRAINTS ALL IMMEDIATE;

DROP INDEX IF EXISTS spaces_one_misty_per_user_idx;
CREATE UNIQUE INDEX spaces_one_canonical_misty_idx
    ON spaces(kind) WHERE kind='misty';

ALTER TABLE space_conversations
    ADD COLUMN support_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;
DROP INDEX IF EXISTS space_conversations_one_misty_support_idx;
CREATE UNIQUE INDEX space_conversations_one_support_user_idx
    ON space_conversations(support_user_id) WHERE kind='misty_support';

ALTER TABLE space_messages ALTER COLUMN expires_at DROP NOT NULL;

ALTER TABLE space_library_uploads
    ADD COLUMN conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE;
CREATE INDEX space_library_uploads_conversation_idx
    ON space_library_uploads(conversation_id) WHERE conversation_id IS NOT NULL;

CREATE TABLE misty_space_config (
    singleton SMALLINT PRIMARY KEY DEFAULT 1 CHECK (singleton=1),
    space_id TEXT NOT NULL UNIQUE REFERENCES spaces(id) ON DELETE RESTRICT,
    support_storage_limit_bytes BIGINT NOT NULL DEFAULT 50000000000
        CHECK (support_storage_limit_bytes>0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE misty_space_operators (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE misty_support_storage_usage (
    singleton SMALLINT PRIMARY KEY DEFAULT 1 CHECK (singleton=1),
    used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes>=0),
    reserved_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reserved_bytes>=0),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO misty_support_storage_usage(singleton) VALUES(1);

CREATE TABLE space_conversation_reads (
    conversation_id TEXT NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_message_seq BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(conversation_id,user_id)
);
CREATE INDEX space_conversation_reads_user_idx
    ON space_conversation_reads(user_id,conversation_id);

ALTER TABLE misty_space_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_space_config FORCE ROW LEVEL SECURITY;
CREATE POLICY misty_space_config_service ON misty_space_config FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE misty_space_operators ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_space_operators FORCE ROW LEVEL SECURITY;
CREATE POLICY misty_space_operators_service ON misty_space_operators FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE misty_support_storage_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_support_storage_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY misty_support_storage_usage_service ON misty_support_storage_usage FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

ALTER TABLE space_conversation_reads ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_reads FORCE ROW LEVEL SECURITY;
CREATE POLICY space_conversation_reads_policy ON space_conversation_reads FOR ALL
    USING (
        misty_rls_is_service() OR user_id=misty_rls_user_id()
    )
    WITH CHECK (
        misty_rls_is_service() OR user_id=misty_rls_user_id()
    );

CREATE OR REPLACE FUNCTION misty_ensure_default_space(candidate_user_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    canonical_space_id TEXT;
    canonical_operator_id TEXT;
    support_conversation_id TEXT := 'space_conversation_misty_' || candidate_user_id;
BEGIN
    IF candidate_user_id IS NULL OR candidate_user_id='' THEN
        RETURN NULL;
    END IF;

    SELECT c.space_id INTO canonical_space_id
    FROM misty_space_config c WHERE c.singleton=1;
    IF canonical_space_id IS NULL THEN
        RETURN NULL;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('misty-space:' || candidate_user_id));

    INSERT INTO space_members(space_id,user_id,role)
    SELECT canonical_space_id,candidate_user_id,
        CASE WHEN EXISTS(
            SELECT 1 FROM spaces s
            WHERE s.id=canonical_space_id AND s.owner_user_id=candidate_user_id
        ) THEN 'owner' ELSE 'member' END
    WHERE EXISTS(
        SELECT 1 FROM users u
        WHERE u.id=candidate_user_id AND u.lifecycle_state='active'
    )
    ON CONFLICT(space_id,user_id) DO NOTHING;

    IF EXISTS(SELECT 1 FROM misty_space_operators WHERE user_id=candidate_user_id) THEN
        RETURN canonical_space_id;
    END IF;

    SELECT s.owner_user_id INTO canonical_operator_id
    FROM spaces s WHERE s.id=canonical_space_id;

    INSERT INTO space_conversations(
        id,space_id,title,created_by_user_id,kind,support_user_id
    )
    SELECT support_conversation_id,canonical_space_id,u.name,
        canonical_operator_id,'misty_support',candidate_user_id
    FROM users u WHERE u.id=candidate_user_id AND u.lifecycle_state='active'
    ON CONFLICT(support_user_id) WHERE kind='misty_support' DO NOTHING;

    SELECT id INTO support_conversation_id
    FROM space_conversations
    WHERE support_user_id=candidate_user_id AND kind='misty_support';

    INSERT INTO space_conversation_members(conversation_id,user_id)
    SELECT support_conversation_id,candidate_user_id
    WHERE support_conversation_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    INSERT INTO space_conversation_members(conversation_id,user_id)
    SELECT support_conversation_id,o.user_id
    FROM misty_space_operators o
    WHERE support_conversation_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    RETURN canonical_space_id;
END
$$;

CREATE OR REPLACE FUNCTION misty_provision_default_space_for_new_user()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
BEGIN
    PERFORM misty_ensure_default_space(NEW.id);
    RETURN NEW;
END
$$;

CREATE TRIGGER users_provision_default_misty_space
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION misty_provision_default_space_for_new_user();

-- Canonical support storage must never flow into the operator's personal pool.
CREATE OR REPLACE FUNCTION refresh_owner_storage_usage(candidate_owner TEXT) RETURNS VOID AS $$
BEGIN
    INSERT INTO owner_storage_usage(owner_user_id,used_bytes,reserved_bytes,version,updated_at)
    SELECT candidate_owner,
        COALESCE((SELECT SUM(su.used_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active' AND s.kind='standard'),0),
        COALESCE((SELECT SUM(su.reserved_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active' AND s.kind='standard'),0),
        1,NOW()
    ON CONFLICT(owner_user_id) DO UPDATE SET
        used_bytes=EXCLUDED.used_bytes,
        reserved_bytes=EXCLUDED.reserved_bytes,
        version=owner_storage_usage.version+1,
        updated_at=NOW();
END
$$ LANGUAGE plpgsql;

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON
            misty_space_config,misty_space_operators,
            misty_support_storage_usage,space_conversation_reads TO misty_app;
        GRANT EXECUTE ON FUNCTION misty_ensure_default_space(TEXT) TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
DROP FUNCTION IF EXISTS misty_provision_default_space_for_new_user();
DROP FUNCTION IF EXISTS misty_ensure_default_space(TEXT);

SET CONSTRAINTS ALL DEFERRED;
DELETE FROM spaces WHERE kind='misty';
DELETE FROM security_domains WHERE id='sd_misty_canonical';

DROP TABLE IF EXISTS space_conversation_reads;
DROP TABLE IF EXISTS misty_support_storage_usage;
DROP TABLE IF EXISTS misty_space_operators;
DROP TABLE IF EXISTS misty_space_config;

DROP INDEX IF EXISTS space_conversations_one_support_user_idx;
DROP INDEX IF EXISTS space_library_uploads_conversation_idx;
ALTER TABLE space_library_uploads DROP COLUMN IF EXISTS conversation_id;
ALTER TABLE space_conversations DROP COLUMN IF EXISTS support_user_id;
CREATE UNIQUE INDEX space_conversations_one_misty_support_idx
    ON space_conversations(space_id) WHERE kind='misty_support';
DROP INDEX IF EXISTS spaces_one_canonical_misty_idx;
CREATE UNIQUE INDEX spaces_one_misty_per_user_idx
    ON spaces(owner_user_id) WHERE kind='misty';
ALTER TABLE space_messages ALTER COLUMN expires_at SET NOT NULL;

CREATE OR REPLACE FUNCTION refresh_owner_storage_usage(candidate_owner TEXT) RETURNS VOID AS $$
BEGIN
    INSERT INTO owner_storage_usage(owner_user_id,used_bytes,reserved_bytes,version,updated_at)
    SELECT candidate_owner,
        COALESCE((SELECT SUM(su.used_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active'),0),
        COALESCE((SELECT SUM(su.reserved_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active'),0),
        1,NOW()
    ON CONFLICT(owner_user_id) DO UPDATE SET
        used_bytes=EXCLUDED.used_bytes,
        reserved_bytes=EXCLUDED.reserved_bytes,
        version=owner_storage_usage.version+1,
        updated_at=NOW();
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
