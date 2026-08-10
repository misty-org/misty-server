-- +goose Up
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

-- Members may view Studio resources and run agents shared with the Space.
-- Creating, editing, publishing, and deleting Studio resources remain
-- owner-only through studio.manage.
UPDATE space_roles
SET permissions = permissions
    || CASE
        WHEN permissions ? 'studio.view' THEN '[]'::jsonb
        ELSE '["studio.view"]'::jsonb
    END
    || CASE
        WHEN permissions ? 'agents.run' THEN '[]'::jsonb
        ELSE '["agents.run"]'::jsonb
    END
WHERE is_everyone;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT set_config('app.rls_mode', 'service', true);

UPDATE space_roles
SET permissions = permissions - 'studio.view' - 'agents.run'
WHERE is_everyone;
-- +goose StatementEnd
