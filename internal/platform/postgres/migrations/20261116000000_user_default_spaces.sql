-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);
SET CONSTRAINTS ALL DEFERRED;

-- Retire the deployment-owned shared Misty Space. If it contains data, keep
-- it as an ordinary private Space owned by its existing owner; every other
-- account is detached and will create its own default through onboarding.
DROP TRIGGER IF EXISTS users_provision_default_misty_space ON users;
DROP FUNCTION IF EXISTS misty_provision_default_space_for_new_user();
DROP FUNCTION IF EXISTS misty_configure_canonical_space(TEXT);
DROP FUNCTION IF EXISTS misty_ensure_default_space(TEXT);

DELETE FROM space_conversation_members cm
USING space_conversations c,spaces s
WHERE cm.conversation_id=c.id AND c.space_id=s.id AND s.kind='misty'
  AND cm.user_id IS DISTINCT FROM s.owner_user_id;

DELETE FROM space_members m
USING spaces s
WHERE m.space_id=s.id AND s.kind='misty' AND m.user_id<>s.owner_user_id;

UPDATE space_conversations
SET kind='standard',support_user_id=NULL,updated_at=NOW()
WHERE kind='misty_support';

UPDATE spaces
SET kind='standard',
    name=CASE WHEN name='Misty' THEN 'Personal Space' ELSE name END,
    updated_at=NOW()
WHERE kind='misty';

DROP TABLE IF EXISTS misty_space_config;
DROP TABLE IF EXISTS misty_space_operators;
DROP TABLE IF EXISTS misty_support_storage_usage;
DROP INDEX IF EXISTS spaces_one_canonical_misty_idx;
DROP INDEX IF EXISTS spaces_one_misty_per_user_idx;
DROP INDEX IF EXISTS space_conversations_one_support_user_idx;

ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_kind_check;
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_kind_check
    CHECK(kind IN ('standard','direct'));
ALTER TABLE space_conversations DROP COLUMN IF EXISTS support_user_id;

-- Canonical support storage is gone, so every active Space contributes to its
-- owner's normal storage pool again.
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

ALTER TABLE spaces ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT FALSE;

-- Do not silently turn an existing Space into the account default. Every
-- account completes the new onboarding contract and deliberately creates its
-- own protected default Space; existing user-owned Spaces remain untouched.

CREATE UNIQUE INDEX spaces_one_default_per_owner_idx
    ON spaces(owner_user_id)
    WHERE is_default AND lifecycle_state='active';

CREATE OR REPLACE FUNCTION misty_protect_default_space()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=public,pg_temp
SET row_security=off
AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF OLD.is_default AND EXISTS(
            SELECT 1 FROM users
            WHERE id=OLD.owner_user_id AND lifecycle_state='active'
        ) THEN
            RAISE EXCEPTION 'default space cannot be deleted or transferred'
                USING ERRCODE='23514';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.is_default AND (
        NOT NEW.is_default OR
        NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id OR
        NEW.lifecycle_state IS DISTINCT FROM 'active'
    ) THEN
        RAISE EXCEPTION 'default space cannot be deleted or transferred'
            USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER spaces_protect_default_update
BEFORE UPDATE OF is_default,owner_user_id,lifecycle_state ON spaces
FOR EACH ROW EXECUTE FUNCTION misty_protect_default_space();

CREATE TRIGGER spaces_protect_default_delete
BEFORE DELETE ON spaces
FOR EACH ROW EXECUTE FUNCTION misty_protect_default_space();

ALTER TABLE spaces DROP CONSTRAINT IF EXISTS spaces_kind_check;
ALTER TABLE spaces DROP COLUMN kind;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION '20261116000000_user_default_spaces is irreversible';
END $$;
-- +goose StatementEnd
