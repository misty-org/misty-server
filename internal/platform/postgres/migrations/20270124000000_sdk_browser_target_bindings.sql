-- +goose Up
-- +goose StatementBegin
ALTER TABLE sdk_target_versions ALTER COLUMN connection_id DROP NOT NULL;
ALTER TABLE sdk_target_versions ALTER COLUMN connection_revision DROP NOT NULL;
ALTER TABLE sdk_target_versions ADD CONSTRAINT sdk_target_route_binding CHECK (
 target->'binding'->>'kind' IS NOT NULL AND (
 (target->'binding'->>'kind'='backend' AND connection_id IS NOT NULL AND connection_revision IS NOT NULL)
 OR (target->'binding'->>'kind'='browser' AND connection_id IS NULL AND connection_revision IS NULL)
));
-- +goose StatementEnd
-- +goose Down
-- Preserve pinned browser targets and histories on rollback.
SELECT 1;
