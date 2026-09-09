-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
-- Shipped server adapters are installed official-app resources. Downloaded
-- providers still require verified manifests in the installation/registration
-- handlers; do not fabricate publisher keys or signatures for host code.
ALTER TABLE sdk_provider_versions DROP CONSTRAINT sdk_provider_versions_user_id_app_id_fkey;
ALTER TABLE sdk_provider_versions ADD CONSTRAINT sdk_provider_versions_installation_fkey
 FOREIGN KEY(user_id,app_id) REFERENCES user_app_installations(user_id,app_id) ON DELETE CASCADE;
ALTER TABLE sdk_target_versions DROP CONSTRAINT sdk_target_route_binding;
ALTER TABLE sdk_target_versions ADD CONSTRAINT sdk_target_route_binding CHECK (target->'binding'->>'kind' IS NOT NULL AND (
 (target->'binding'->>'kind'='backend' AND connection_id IS NOT NULL AND connection_revision IS NOT NULL)
 OR (target->'binding'->>'kind'='browser' AND connection_id IS NULL AND connection_revision IS NULL)
 OR (target->'binding'->>'kind'='resource' AND provider_id='planner/tasks' AND connection_id IS NULL AND connection_revision IS NULL)
));
-- +goose StatementEnd
-- +goose Down
-- Preserve invocation and provider recovery records.
SELECT 1;
