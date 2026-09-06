-- +goose Up
-- +goose StatementBegin
-- Default Spaces remain protected in ordinary requests. Native account cleanup
-- may schedule one only after its account and remote cleanup have been fenced.
CREATE OR REPLACE FUNCTION misty_protect_default_space() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET row_security=off AS $$
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
-- +goose Down
-- Forward only: keep the protection compatible with scheduled native requests.
SELECT 1;
