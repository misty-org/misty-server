-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_agents
    ADD COLUMN role text NOT NULL DEFAULT '',
    ADD COLUMN description text NOT NULL DEFAULT '',
    ADD COLUMN instructions text NOT NULL DEFAULT '',
    ADD COLUMN soul text NOT NULL DEFAULT '',
    ADD COLUMN icon text NOT NULL DEFAULT '',
    ADD COLUMN avatar jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN voice_id text NOT NULL DEFAULT '',
    ADD COLUMN engine_id text NOT NULL DEFAULT '',
    ADD COLUMN engine_model text NOT NULL DEFAULT '',
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT '',
    ADD COLUMN origin text NOT NULL DEFAULT '',
    ADD COLUMN revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_by_user_id text REFERENCES users(id) ON DELETE SET NULL,
    ADD CONSTRAINT space_agents_role_check CHECK (char_length(role) <= 160),
    ADD CONSTRAINT space_agents_description_check CHECK (char_length(description) <= 2000),
    ADD CONSTRAINT space_agents_instructions_check CHECK (char_length(instructions) <= 16000),
    ADD CONSTRAINT space_agents_soul_check CHECK (char_length(soul) <= 100000),
    ADD CONSTRAINT space_agents_icon_check CHECK (char_length(icon) <= 128),
    ADD CONSTRAINT space_agents_avatar_check CHECK (jsonb_typeof(avatar) = 'object' AND octet_length(avatar::text) <= 4096),
    ADD CONSTRAINT space_agents_voice_check CHECK (voice_id ~ '^[a-z0-9-]{0,64}$'),
    ADD CONSTRAINT space_agents_engine_check CHECK (engine_id ~ '^[a-z0-9_-]{0,40}$'),
    ADD CONSTRAINT space_agents_engine_model_check CHECK (char_length(engine_model) <= 200),
    ADD CONSTRAINT space_agents_effort_check CHECK (char_length(reasoning_effort) <= 32),
    ADD CONSTRAINT space_agents_origin_check CHECK (char_length(origin) <= 200),
    ADD CONSTRAINT space_agents_revision_check CHECK (revision >= 0);

UPDATE space_agents SET updated_at = added_at;

DELETE FROM space_agents a USING space_agents b
    WHERE a.space_id = b.space_id AND a.agent_id = b.agent_id
      AND (a.added_at, a.owner_user_id) > (b.added_at, b.owner_user_id);
CREATE UNIQUE INDEX space_agents_space_agent ON space_agents(space_id, agent_id);

-- +goose Down
DROP INDEX IF EXISTS space_agents_space_agent;
ALTER TABLE space_agents
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS instructions,
    DROP COLUMN IF EXISTS soul,
    DROP COLUMN IF EXISTS icon,
    DROP COLUMN IF EXISTS avatar,
    DROP COLUMN IF EXISTS voice_id,
    DROP COLUMN IF EXISTS engine_id,
    DROP COLUMN IF EXISTS engine_model,
    DROP COLUMN IF EXISTS reasoning_effort,
    DROP COLUMN IF EXISTS origin,
    DROP COLUMN IF EXISTS revision,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS updated_by_user_id;
