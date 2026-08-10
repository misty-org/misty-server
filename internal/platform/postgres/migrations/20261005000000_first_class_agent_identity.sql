-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE personal_agents
    ADD COLUMN role TEXT NOT NULL DEFAULT '' CHECK(char_length(role) <= 80),
    ADD COLUMN avatar JSONB NOT NULL DEFAULT '{"kind":"preset","preset_id":"bot","accent":"indigo"}'::jsonb
        CHECK(jsonb_typeof(avatar)='object');

ALTER TABLE personal_agent_versions
    ADD COLUMN role TEXT NOT NULL DEFAULT '' CHECK(char_length(role) <= 80),
    ADD COLUMN avatar JSONB NOT NULL DEFAULT '{"kind":"preset","preset_id":"bot","accent":"indigo"}'::jsonb
        CHECK(jsonb_typeof(avatar)='object');

ALTER TABLE personal_agent_space_grants
    ADD COLUMN space_role TEXT NOT NULL DEFAULT '' CHECK(char_length(space_role) <= 80);

UPDATE personal_agents
SET avatar=jsonb_build_object(
    'kind','preset',
    'preset_id',COALESCE(NULLIF(icon,''),'bot'),
    'accent','indigo'
);

UPDATE personal_agent_versions
SET avatar=jsonb_build_object(
    'kind','preset',
    'preset_id',COALESCE(NULLIF(icon,''),'bot'),
    'accent','indigo'
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE personal_agent_space_grants DROP COLUMN IF EXISTS space_role;
ALTER TABLE personal_agent_versions DROP COLUMN IF EXISTS avatar, DROP COLUMN IF EXISTS role;
ALTER TABLE personal_agents DROP COLUMN IF EXISTS avatar, DROP COLUMN IF EXISTS role;
-- +goose StatementEnd
