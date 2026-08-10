-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Misty is a control-plane managed Space. Every active account is a Space
-- member, but receives exactly one private support conversation. Space-wide
-- collaboration and lifecycle operations are rejected in the application
-- layer for this Space.
ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_kind_check;
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_kind_check
    CHECK(kind IN ('standard','direct','misty_support'));

CREATE UNIQUE INDEX IF NOT EXISTS spaces_one_canonical_misty_idx
    ON spaces(kind) WHERE kind='misty';
CREATE UNIQUE INDEX IF NOT EXISTS space_conversations_one_support_user_idx
    ON space_conversations(support_user_id) WHERE kind='misty_support';

-- Preserve the per-account Spaces created by the retired model, while making
-- the canonical Space the only one presented as Misty.
UPDATE spaces s
SET name='Personal Space',updated_at=NOW()
WHERE s.kind='standard' AND s.name='Misty'
  AND EXISTS(
      SELECT 1 FROM space_members m
      WHERE m.space_id=s.id AND m.user_id=s.owner_user_id AND m.role='owner'
  )
  AND s.id='space_default_' || md5(s.owner_user_id);

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
    support_conversation_id TEXT := 'space_conversation_misty_' || md5(candidate_user_id);
BEGIN
    IF candidate_user_id IS NULL OR candidate_user_id='' THEN RETURN NULL; END IF;

    SELECT c.space_id INTO canonical_space_id
    FROM misty_space_config c WHERE c.singleton=1;
    IF canonical_space_id IS NULL THEN
        -- The server configures the canonical Space after validating the
        -- immutable MISTY_OPERATOR_USER_ID at startup.
        RETURN NULL;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('misty-space:' || candidate_user_id));

    INSERT INTO space_members(space_id,user_id,role)
    SELECT canonical_space_id,candidate_user_id,
        CASE WHEN s.owner_user_id=candidate_user_id THEN 'owner' ELSE 'member' END
    FROM spaces s JOIN users u ON u.id=candidate_user_id
    WHERE s.id=canonical_space_id AND u.lifecycle_state='active'
    ON CONFLICT(space_id,user_id) DO UPDATE SET
        role=CASE WHEN EXCLUDED.role='owner' THEN 'owner' ELSE space_members.role END;

    IF EXISTS(SELECT 1 FROM misty_space_operators WHERE user_id=candidate_user_id) THEN
        RETURN canonical_space_id;
    END IF;

    SELECT s.owner_user_id INTO canonical_operator_id
    FROM spaces s WHERE s.id=canonical_space_id;

    INSERT INTO space_conversations(
        id,space_id,title,created_by_user_id,kind,support_user_id,visible_to_space
    )
    SELECT support_conversation_id,canonical_space_id,u.name,
        canonical_operator_id,'misty_support',candidate_user_id,FALSE
    FROM users u WHERE u.id=candidate_user_id AND u.lifecycle_state='active'
    ON CONFLICT(support_user_id) WHERE kind='misty_support' DO UPDATE SET
        title=EXCLUDED.title,updated_at=NOW();

    SELECT id INTO support_conversation_id
    FROM space_conversations
    WHERE support_user_id=candidate_user_id AND kind='misty_support';

    INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind)
    SELECT support_conversation_id,candidate_user_id,'person'
    WHERE support_conversation_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind)
    SELECT support_conversation_id,o.user_id,'person'
    FROM misty_space_operators o
    WHERE support_conversation_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    RETURN canonical_space_id;
END
$$;

CREATE OR REPLACE FUNCTION misty_configure_canonical_space(candidate_operator_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    canonical_space_id TEXT := 'space_misty_canonical';
    canonical_domain_id TEXT := 'sd_misty_canonical';
    canonical_owner_id TEXT;
    candidate_user_id TEXT;
    needs_backfill BOOLEAN;
BEGIN
    IF NOT EXISTS(
        SELECT 1 FROM users
        WHERE id=candidate_operator_id AND lifecycle_state='active'
    ) THEN
        RETURN NULL;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('misty-space:configure'));
    SET CONSTRAINTS ALL DEFERRED;

    SELECT NOT EXISTS(
        SELECT 1
        FROM misty_space_config c
        JOIN spaces s ON s.id=c.space_id
        WHERE c.singleton=1 AND s.id=canonical_space_id AND s.kind='misty'
    ) INTO needs_backfill;

    SELECT s.owner_user_id INTO canonical_owner_id
    FROM misty_space_config c JOIN spaces s ON s.id=c.space_id
    WHERE c.singleton=1;
    canonical_owner_id := COALESCE(canonical_owner_id,candidate_operator_id);

    INSERT INTO misty_space_operators(user_id) VALUES(candidate_operator_id)
    ON CONFLICT(user_id) DO NOTHING;

    INSERT INTO security_domains(id,kind,owner_user_id,space_id)
    VALUES(canonical_domain_id,'space',canonical_owner_id,canonical_space_id)
    ON CONFLICT(id) DO UPDATE SET
        owner_user_id=EXCLUDED.owner_user_id,space_id=EXCLUDED.space_id,updated_at=NOW();

    INSERT INTO spaces(id,owner_user_id,name,security_domain_id,kind)
    VALUES(canonical_space_id,canonical_owner_id,'Misty',canonical_domain_id,'misty')
    ON CONFLICT(id) DO UPDATE SET
        owner_user_id=EXCLUDED.owner_user_id,name='Misty',
        security_domain_id=EXCLUDED.security_domain_id,kind='misty',
        lifecycle_state='active',deletion_requested_at=NULL,
        permanent_delete_after=NULL,updated_at=NOW();

    INSERT INTO space_storage_usage(space_id) VALUES(canonical_space_id)
    ON CONFLICT(space_id) DO NOTHING;

    INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
    VALUES(
        'role_misty_canonical',canonical_space_id,'@everyone',TRUE,
        '["space.view","messages.read","messages.write","attachments.upload"]'::jsonb
    )
    ON CONFLICT(id) DO UPDATE SET
        permissions=EXCLUDED.permissions,version=space_roles.version+1,updated_at=NOW();

    INSERT INTO misty_space_config(singleton,space_id)
    VALUES(1,canonical_space_id)
    ON CONFLICT(singleton) DO UPDATE SET space_id=EXCLUDED.space_id,updated_at=NOW();

    -- Existing accounts need one initial backfill. Subsequent server starts
    -- only repair the configured operator; new accounts use the insert trigger.
    IF needs_backfill THEN
        FOR candidate_user_id IN
            SELECT id FROM users WHERE lifecycle_state='active' ORDER BY id
        LOOP
            PERFORM misty_ensure_default_space(candidate_user_id);
        END LOOP;
    ELSE
        PERFORM misty_ensure_default_space(candidate_operator_id);
    END IF;

    -- A newly added operator must join every existing support conversation.
    INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind)
    SELECT c.id,candidate_operator_id,'person'
    FROM space_conversations c
    WHERE c.space_id=canonical_space_id AND c.kind='misty_support'
    ON CONFLICT DO NOTHING;

    RETURN canonical_space_id;
END
$$;

CREATE OR REPLACE FUNCTION misty_provision_default_space_for_new_user()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp SET row_security = off AS $$
BEGIN
    PERFORM misty_ensure_default_space(NEW.id);
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
CREATE TRIGGER users_provision_default_misty_space
AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION misty_provision_default_space_for_new_user();

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT EXECUTE ON FUNCTION misty_ensure_default_space(TEXT) TO misty_app;
        GRANT EXECUTE ON FUNCTION misty_configure_canonical_space(TEXT) TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Preserve per-account data on rollback. Remove only the canonical support
-- topology; an older server can provision ordinary defaults on demand.
DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
DROP FUNCTION IF EXISTS misty_provision_default_space_for_new_user();
DROP FUNCTION IF EXISTS misty_configure_canonical_space(TEXT);
DROP FUNCTION IF EXISTS misty_ensure_default_space(TEXT);

SET CONSTRAINTS ALL DEFERRED;
DELETE FROM spaces WHERE id='space_misty_canonical';
DELETE FROM security_domains WHERE id='sd_misty_canonical';
DELETE FROM misty_space_config WHERE singleton=1;

DROP INDEX IF EXISTS spaces_one_canonical_misty_idx;
ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_kind_check;
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_kind_check
    CHECK(kind IN ('standard','direct'));
-- +goose StatementEnd
