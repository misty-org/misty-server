-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
-- Keep connected-provider definitions and immutable execution revisions under
-- their account. Retired install records no longer grant or revoke this access.
ALTER TABLE public.sdk_provider_registrations DROP CONSTRAINT IF EXISTS sdk_provider_registrations_user_id_app_id_fkey;
ALTER TABLE public.sdk_provider_registrations ADD CONSTRAINT sdk_provider_registrations_account_fkey FOREIGN KEY(user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.sdk_backend_connections DROP CONSTRAINT IF EXISTS sdk_backend_connections_user_id_app_id_fkey;
ALTER TABLE public.sdk_backend_connections ADD CONSTRAINT sdk_backend_connections_account_fkey FOREIGN KEY(user_id) REFERENCES public.users(id) ON DELETE CASCADE;
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['app_data_deletion_jobs','app_install_events','app_personal_records','app_runtime_sessions','misty_agent_app_assignments','sdk_app_manifest_versions','sdk_app_publishers','space_app_installations','user_app_installations'] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
 IF to_regprocedure('public.advance_app_authority_generation()') IS NOT NULL THEN
  ALTER FUNCTION public.advance_app_authority_generation() SET SCHEMA misty_archive;
 END IF;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  REVOKE ALL ON SCHEMA misty_archive FROM misty_app;
  REVOKE ALL ON ALL TABLES IN SCHEMA misty_archive FROM misty_app;
  REVOKE ALL ON ALL SEQUENCES IN SCHEMA misty_archive FROM misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retired app data is archived; use operator recovery rather than re-enabling app credentials'; END $$;
-- +goose StatementEnd
