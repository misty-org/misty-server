-- +goose Up
-- A personal template now stores the structure captured from a Space (task
-- titles, note outlines and collection names). The empty app list it held
-- before is retired along with per-template apps.
ALTER TABLE personal_space_templates
    ADD COLUMN definition jsonb NOT NULL DEFAULT '{}'::jsonb
        CONSTRAINT personal_space_templates_definition_check CHECK (jsonb_typeof(definition) = 'object');
ALTER TABLE personal_space_templates DROP COLUMN apps;

-- +goose Down
ALTER TABLE personal_space_templates
    ADD COLUMN apps jsonb NOT NULL DEFAULT '[]'::jsonb
        CONSTRAINT personal_space_templates_apps_check CHECK (jsonb_typeof(apps) = 'array');
ALTER TABLE personal_space_templates DROP COLUMN definition;
