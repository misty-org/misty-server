-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Transfers is a Files subsection. Removing the obsolete installation also
-- cascades its short-lived runtime sessions, deletion job, and personal records.
DELETE FROM user_app_installations WHERE app_id='transfers';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- App installations are user choices; a rollback must not recreate one.
SELECT 1;
-- +goose StatementEnd
