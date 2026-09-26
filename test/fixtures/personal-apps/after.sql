BEGIN;
SELECT set_config('app.rls_mode','service',true);
DO $$ BEGIN
 IF (SELECT count(*) FROM app_personal_record_imports WHERE user_id IN ('migration_owner','migration_member'))<>3 THEN RAISE EXCEPTION 'Migration lost original records'; END IF;
 IF (SELECT count(*) FROM app_personal_records WHERE user_id IN ('migration_owner','migration_member'))<>2 THEN RAISE EXCEPTION 'Account keys did not consolidate'; END IF;
 IF (SELECT data->>'value' FROM app_personal_records WHERE user_id='migration_owner' AND app_id='example' AND record_key='same-key')<>'newer' THEN RAISE EXCEPTION 'Canonical value is not deterministic'; END IF;
 IF EXISTS(SELECT 1 FROM app_runtime_sessions WHERE token_hash=repeat('e',64)) THEN RAISE EXCEPTION 'Legacy token survived migration'; END IF;
 IF (SELECT count(*) FROM user_app_installations WHERE user_id IN ('migration_owner','migration_member') AND consent_required AND granted_scopes='[]')<>2 THEN RAISE EXCEPTION 'Manager consent became personal authority'; END IF;
 IF (SELECT count(*) FROM space_members WHERE space_id IN ('migration_space_one','migration_space_two'))<>3 THEN RAISE EXCEPTION 'Memberships changed'; END IF;
END $$;
COMMIT;
