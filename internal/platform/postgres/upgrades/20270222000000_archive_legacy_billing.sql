-- +goose Up
-- Existing customer data is preserved verbatim. The application uses only the
-- adapter; the operator exports/reconciles this archive before enabling billing.
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
DO $$ DECLARE relation text; held bigint; BEGIN
 IF to_regclass('public.hosted_ai_reservations') IS NOT NULL THEN
  SELECT count(*) INTO held FROM public.hosted_ai_reservations WHERE status='reserved';
  IF held>0 THEN RAISE EXCEPTION 'Drain legacy billing reservations before archival'; END IF;
 END IF;
 FOREACH relation IN ARRAY ARRAY[
  'stripe_subscriptions','stripe_purchases','stripe_subscription_checkout_attempts','stripe_webhook_events',
  'hosted_ai_wallets','hosted_ai_reservations','hosted_ai_usage_ledger','space_hosted_ai_wallets',
  'credit_wallets','credit_reservations','credit_ledger','credit_purchases',
  'license_lifetime_grants','payment_entitlement_inbox','payment_entitlement_projections'
 ] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Re-enabling a legacy billing writer requires an explicit operator recovery.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore billing only through a reconciled single-writer recovery'; END $$;
-- +goose StatementEnd
