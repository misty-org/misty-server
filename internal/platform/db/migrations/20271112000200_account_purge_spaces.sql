-- +goose Up
-- The full purge of a deleted account (internal/platform/db/account_purge.go)
-- marks the Spaces it owned, the personal Space included, for the Space purge.
-- The personal Space stays protected while its owner's account is active.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.misty_protect_default_space() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF OLD.is_default AND EXISTS(SELECT 1 FROM public.users WHERE id=OLD.owner_user_id AND lifecycle_state='active') THEN
      RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.is_default AND (NOT NEW.is_default OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id) THEN
    RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514';
  END IF;
  IF OLD.is_default AND NEW.lifecycle_state IS DISTINCT FROM 'active' THEN
    IF NOT (public.misty_rls_is_service() AND NEW.lifecycle_state='pending_deletion'
      AND EXISTS(SELECT 1 FROM public.users u WHERE u.id=OLD.owner_user_id AND u.lifecycle_state IN ('pending_deletion','deleted')))
    THEN RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514'; END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.misty_protect_default_space() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF OLD.is_default AND EXISTS(SELECT 1 FROM public.users WHERE id=OLD.owner_user_id AND lifecycle_state='active') THEN
      RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.is_default AND (NOT NEW.is_default OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id) THEN
    RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514';
  END IF;
  IF OLD.is_default AND NEW.lifecycle_state IS DISTINCT FROM 'active' THEN
    IF NOT (public.misty_rls_is_service() AND OLD.lifecycle_state IN ('active','pending_deletion') AND NEW.lifecycle_state='pending_deletion'
      AND EXISTS(SELECT 1 FROM public.users u JOIN public.account_deletion_requests r ON r.user_id=u.id
        JOIN public.account_deletion_steps s ON s.request_id=r.id AND s.step='local'
        WHERE u.id=OLD.owner_user_id AND u.lifecycle_state='pending_deletion' AND r.cleanup_owner='native' AND r.status='processing'
          AND s.state='processing' AND s.lease_expires_at>clock_timestamp()
          AND EXISTS(SELECT 1 FROM public.account_deletion_steps p WHERE p.request_id=r.id AND p.step='payments' AND p.state='completed')
          AND EXISTS(SELECT 1 FROM public.account_deletion_steps p WHERE p.request_id=r.id AND p.step='providers' AND p.state='completed')))
    THEN RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514'; END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
