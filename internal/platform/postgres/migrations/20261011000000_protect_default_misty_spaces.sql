-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

-- Default Misty Spaces that were deleted by ordinary users were accepted by
-- older application code even though that lifecycle operation is reserved for
-- operators. Restore those Spaces before replacing the provisioning function
-- so affected accounts can load their snapshot again immediately.
UPDATE spaces s
SET lifecycle_state='active',
    deletion_requested_at=NULL,
    permanent_delete_after=NULL,
    updated_at=NOW()
WHERE s.lifecycle_state='pending_deletion'
  AND s.id LIKE 'space_default_%'
  AND s.security_domain_id LIKE 'sd_default_%'
  AND NOT EXISTS(
      SELECT 1 FROM misty_space_operators o WHERE o.user_id=s.owner_user_id
  );

-- Provisioning is an idempotent seed, not an invariant on the current name or
-- lifecycle state. Resolve the stable default-domain identity first so a
-- rename, an operator deletion, or a partial client snapshot never attempts to
-- insert the same security-domain primary key twice.
CREATE OR REPLACE FUNCTION misty_ensure_default_space(candidate_user_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    default_space_id TEXT := 'space_default_' || md5(candidate_user_id);
    default_domain_id TEXT := 'sd_default_' || md5(candidate_user_id);
    existing_space_id TEXT;
BEGIN
    IF candidate_user_id IS NULL OR candidate_user_id='' THEN RETURN NULL; END IF;
    PERFORM pg_advisory_xact_lock(hashtext('default-space:' || candidate_user_id));

    SELECT s.id INTO existing_space_id
    FROM security_domains d
    JOIN spaces s ON s.id=d.space_id
    WHERE d.id=default_domain_id
    LIMIT 1;
    IF existing_space_id IS NOT NULL THEN RETURN existing_space_id; END IF;

    -- Preserve compatibility for accounts whose default Space predates the
    -- deterministic identity introduced in 20261009.
    SELECT id INTO existing_space_id FROM spaces
        WHERE owner_user_id=candidate_user_id AND name='Misty' AND lifecycle_state='active'
        ORDER BY created_at LIMIT 1;
    IF existing_space_id IS NOT NULL THEN RETURN existing_space_id; END IF;

    INSERT INTO security_domains(id,kind,owner_user_id,space_id)
        VALUES(default_domain_id,'space',candidate_user_id,default_space_id);
    INSERT INTO spaces(id,owner_user_id,name,security_domain_id,kind)
        VALUES(default_space_id,candidate_user_id,'Misty',default_domain_id,'standard');
    INSERT INTO space_storage_usage(space_id) VALUES(default_space_id);
    INSERT INTO space_members(space_id,user_id,role) VALUES(default_space_id,candidate_user_id,'owner');
    INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
        VALUES('role_default_'||md5(candidate_user_id),default_space_id,'@everyone',TRUE,
        '["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage","agents.run"]'::jsonb);
    RETURN default_space_id;
END
$$;

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT EXECUTE ON FUNCTION misty_ensure_default_space(TEXT) TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Data restoration is intentionally not reversed. Rolling back only restores
-- the previous provisioning behavior.
CREATE OR REPLACE FUNCTION misty_ensure_default_space(candidate_user_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    default_space_id TEXT := 'space_default_' || md5(candidate_user_id);
    default_domain_id TEXT := 'sd_default_' || md5(candidate_user_id);
    existing_space_id TEXT;
BEGIN
    IF candidate_user_id IS NULL OR candidate_user_id='' THEN RETURN NULL; END IF;
    PERFORM pg_advisory_xact_lock(hashtext('default-space:' || candidate_user_id));
    SELECT id INTO existing_space_id FROM spaces
        WHERE owner_user_id=candidate_user_id AND name='Misty' AND lifecycle_state='active'
        ORDER BY created_at LIMIT 1;
    IF existing_space_id IS NOT NULL THEN RETURN existing_space_id; END IF;
    INSERT INTO security_domains(id,kind,owner_user_id,space_id)
        VALUES(default_domain_id,'space',candidate_user_id,default_space_id);
    INSERT INTO spaces(id,owner_user_id,name,security_domain_id,kind)
        VALUES(default_space_id,candidate_user_id,'Misty',default_domain_id,'standard');
    INSERT INTO space_storage_usage(space_id) VALUES(default_space_id);
    INSERT INTO space_members(space_id,user_id,role) VALUES(default_space_id,candidate_user_id,'owner');
    INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
        VALUES('role_default_'||md5(candidate_user_id),default_space_id,'@everyone',TRUE,
        '["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage","agents.run"]'::jsonb);
    RETURN default_space_id;
END
$$;
-- +goose StatementEnd
