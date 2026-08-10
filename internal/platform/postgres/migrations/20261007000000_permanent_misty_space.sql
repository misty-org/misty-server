-- +goose Up
-- +goose StatementBegin
ALTER TABLE spaces
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'standard'
        CHECK (kind IN ('standard','misty'));

CREATE UNIQUE INDEX spaces_one_misty_per_user_idx
    ON spaces(owner_user_id) WHERE kind='misty';

ALTER TABLE space_conversations
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'standard'
        CHECK (kind IN ('standard','misty_support'));

CREATE UNIQUE INDEX space_conversations_one_misty_support_idx
    ON space_conversations(space_id) WHERE kind='misty_support';

CREATE OR REPLACE FUNCTION misty_ensure_default_space(candidate_user_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    default_space_id TEXT := 'space_misty_' || candidate_user_id;
    default_domain_id TEXT := 'sd_misty_' || candidate_user_id;
    default_conversation_id TEXT := 'space_conversation_misty_' || candidate_user_id;
    existing_space_id TEXT;
BEGIN
    IF candidate_user_id IS NULL OR candidate_user_id='' THEN
        RETURN NULL;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('misty-space:' || candidate_user_id));

    SELECT id INTO existing_space_id
    FROM spaces
    WHERE owner_user_id=candidate_user_id AND kind='misty'
    LIMIT 1;
    IF existing_space_id IS NOT NULL THEN
        RETURN existing_space_id;
    END IF;

    INSERT INTO security_domains(id,kind,owner_user_id,space_id)
    VALUES(default_domain_id,'space',candidate_user_id,default_space_id);
    INSERT INTO spaces(id,owner_user_id,name,security_domain_id,kind)
    VALUES(default_space_id,candidate_user_id,'Misty',default_domain_id,'misty');
    INSERT INTO space_storage_usage(space_id) VALUES(default_space_id);
    INSERT INTO space_members(space_id,user_id,role)
    VALUES(default_space_id,candidate_user_id,'owner');
    INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
    VALUES(
        'role_misty_' || candidate_user_id,default_space_id,'@everyone',TRUE,
        '["space.view","messages.read"]'::jsonb
    );
    INSERT INTO space_conversations(id,space_id,title,created_by_user_id,kind)
    VALUES(default_conversation_id,default_space_id,'Misty Support',candidate_user_id,'misty_support');
    INSERT INTO space_conversation_members(conversation_id,user_id)
    VALUES(default_conversation_id,candidate_user_id);

    RETURN default_space_id;
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

SELECT misty_ensure_default_space(id)
FROM users
WHERE lifecycle_state='active';

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
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
DELETE FROM security_domains WHERE id LIKE 'sd_misty_%';

DROP INDEX IF EXISTS space_conversations_one_misty_support_idx;
ALTER TABLE space_conversations DROP COLUMN IF EXISTS kind;
DROP INDEX IF EXISTS spaces_one_misty_per_user_idx;
ALTER TABLE spaces DROP COLUMN IF EXISTS kind;
-- +goose StatementEnd
