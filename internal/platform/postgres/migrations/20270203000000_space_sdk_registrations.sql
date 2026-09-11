-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode','service',true);
ALTER TABLE sdk_provider_versions DROP CONSTRAINT IF EXISTS sdk_provider_versions_installation_fkey;
-- Cached immutable manifests are not installation authority.
ALTER TABLE sdk_app_publishers DROP CONSTRAINT IF EXISTS sdk_app_publishers_user_id_app_id_fkey;
DELETE FROM sdk_provider_registrations;
ALTER TABLE sdk_provider_registrations DROP CONSTRAINT sdk_provider_registrations_user_id_app_id_app_version_fkey;
ALTER TABLE sdk_provider_registrations ADD COLUMN space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE sdk_provider_registrations DROP CONSTRAINT sdk_provider_registrations_pkey;
ALTER TABLE sdk_provider_registrations ADD PRIMARY KEY(user_id,space_id,provider_id);
ALTER TABLE sdk_provider_registrations ADD FOREIGN KEY(space_id,app_id) REFERENCES space_app_installations(space_id,app_id) ON DELETE CASCADE;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to downgrade the Space app model'; END $$;
-- +goose StatementEnd
