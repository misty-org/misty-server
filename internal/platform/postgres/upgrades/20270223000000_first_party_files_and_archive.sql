-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
-- File sharing is owned by the account and its trusted devices. Pairing,
-- revocation and fresh endpoint checks remain mandatory at ticket issuance.
ALTER TABLE space_device_presence DROP CONSTRAINT IF EXISTS space_device_presence_owner_user_id_app_id_fkey;
DROP POLICY IF EXISTS space_device_presence_owner_read ON space_device_presence;
CREATE POLICY space_device_presence_owner_read ON space_device_presence FOR SELECT
 USING(owner_user_id=misty_rls_user_id());
-- Old presence cannot issue tickets with the first-party protocol generation.
-- Preserve records until each device publishes fresh presence.
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['payment_purchase_reversals','onboarding_completions'] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore archived data only through operator recovery'; END $$;
-- +goose StatementEnd
