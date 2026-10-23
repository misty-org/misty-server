-- +goose Up
-- People are known by their username alone; the separate display name is gone.
-- A new personal Space is titled from the username instead.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_ensure_personal_space(target_user_id text)
 RETURNS text
 LANGUAGE plpgsql
 SECURITY DEFINER
 SET search_path TO 'pg_catalog', 'public', 'pg_temp'
 SET row_security TO 'off'
AS $function$
DECLARE
  existing text;
  owner_username text;
  new_space_id text := 'space_' || gen_random_uuid()::text;
  new_domain_id text := 'sd_' || gen_random_uuid()::text;
BEGIN
  -- Same lock the app takes when it creates a Space for this owner.
  PERFORM pg_advisory_xact_lock(hashtext('spaces:owner:' || target_user_id));
  SELECT id INTO existing FROM spaces
    WHERE owner_user_id=target_user_id AND is_default AND lifecycle_state='active';
  IF existing IS NOT NULL THEN
    RETURN existing;
  END IF;
  SELECT NULLIF(btrim(username), '') INTO owner_username
    FROM users WHERE id=target_user_id AND lifecycle_state='active';
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES(new_domain_id,'space',target_user_id,new_space_id);
  INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
    VALUES(new_space_id,target_user_id,COALESCE(owner_username || '''s Space','Personal Space'),new_domain_id,TRUE);
  INSERT INTO space_storage_usage(space_id) VALUES(new_space_id);
  INSERT INTO space_members(space_id,user_id,role) VALUES(new_space_id,target_user_id,'owner');
  RETURN new_space_id;
END $function$;
-- +goose StatementEnd

ALTER TABLE users DROP COLUMN name;

-- +goose Down
-- The dropped names are not recovered; accounts come back with an empty one.
ALTER TABLE users ADD COLUMN name text DEFAULT ''::text NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_ensure_personal_space(target_user_id text)
 RETURNS text
 LANGUAGE plpgsql
 SECURITY DEFINER
 SET search_path TO 'pg_catalog', 'public', 'pg_temp'
 SET row_security TO 'off'
AS $function$
DECLARE
  existing text;
  first_name text;
  new_space_id text := 'space_' || gen_random_uuid()::text;
  new_domain_id text := 'sd_' || gen_random_uuid()::text;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('spaces:owner:' || target_user_id));
  SELECT id INTO existing FROM spaces
    WHERE owner_user_id=target_user_id AND is_default AND lifecycle_state='active';
  IF existing IS NOT NULL THEN
    RETURN existing;
  END IF;
  SELECT left(NULLIF(split_part(btrim(name), ' ', 1), ''), 64) INTO first_name
    FROM users WHERE id=target_user_id AND lifecycle_state='active';
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES(new_domain_id,'space',target_user_id,new_space_id);
  INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
    VALUES(new_space_id,target_user_id,COALESCE(first_name || '''s Space','Personal Space'),new_domain_id,TRUE);
  INSERT INTO space_storage_usage(space_id) VALUES(new_space_id);
  INSERT INTO space_members(space_id,user_id,role) VALUES(new_space_id,target_user_id,'owner');
  RETURN new_space_id;
END $function$;
-- +goose StatementEnd
