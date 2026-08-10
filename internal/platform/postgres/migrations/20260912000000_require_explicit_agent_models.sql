-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

UPDATE personal_agents
SET model_mode = 'pinned',
    model_id = 'google/gemini-2.5-flash-lite',
    version = version + 1,
    updated_at = NOW()
WHERE model_mode <> 'pinned' OR model_id = '';

UPDATE agent_conversations
SET model_id = 'google/gemini-2.5-flash-lite',
    model_catalog_version = 'gateway-live-v2'
WHERE model_id = '';

ALTER TABLE personal_agents
    DROP CONSTRAINT IF EXISTS personal_agents_model_mode_check,
    DROP CONSTRAINT IF EXISTS personal_agents_check;
ALTER TABLE personal_agents
    ALTER COLUMN model_mode SET DEFAULT 'pinned',
    ADD CONSTRAINT personal_agents_model_mode_pinned_check CHECK (model_mode = 'pinned'),
    ADD CONSTRAINT personal_agents_model_id_required_check CHECK (char_length(model_id) > 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE personal_agents
    DROP CONSTRAINT IF EXISTS personal_agents_model_mode_pinned_check,
    DROP CONSTRAINT IF EXISTS personal_agents_model_id_required_check;
ALTER TABLE personal_agents
    ALTER COLUMN model_mode SET DEFAULT 'automatic',
    ADD CONSTRAINT personal_agents_model_mode_check CHECK (model_mode IN ('automatic','pinned')),
    ADD CONSTRAINT personal_agents_check CHECK (model_mode='automatic' OR char_length(model_id)>0);
-- +goose StatementEnd
