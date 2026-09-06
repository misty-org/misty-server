-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- This is an intentional one-time reset for the new Core + Store model.
-- Existing collaborative Spaces and their content are preserved, but no
-- existing Space is silently selected as the protected account default.
-- Every account runs the new onboarding transaction and explicitly creates
-- a new default Space. App choices and app-private data start clean as well.
DELETE FROM onboarding_completions;
DELETE FROM app_install_events;
DELETE FROM user_app_installations;

-- Hosts may update a downloaded package without changing its pin position.
-- Older development databases created the event constraint before the
-- explicit update event was added.
ALTER TABLE app_install_events DROP CONSTRAINT IF EXISTS app_install_events_event_type_check;
ALTER TABLE app_install_events ADD CONSTRAINT app_install_events_event_type_check
    CHECK(event_type IN ('installed','updated','restored','pinned','unpinned','uninstalled','purge_started','purged','purge_failed','permissions_changed'));

DROP TRIGGER IF EXISTS spaces_protect_default_update ON spaces;
DROP TRIGGER IF EXISTS spaces_protect_default_delete ON spaces;
UPDATE spaces SET is_default=FALSE,updated_at=NOW() WHERE is_default;
CREATE TRIGGER spaces_protect_default_update
BEFORE UPDATE OF is_default,owner_user_id,lifecycle_state ON spaces
FOR EACH ROW EXECUTE FUNCTION misty_protect_default_space();
CREATE TRIGGER spaces_protect_default_delete
BEFORE DELETE ON spaces
FOR EACH ROW EXECUTE FUNCTION misty_protect_default_space();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION '20261118000000_reset_app_onboarding is irreversible';
END $$;
-- +goose StatementEnd
