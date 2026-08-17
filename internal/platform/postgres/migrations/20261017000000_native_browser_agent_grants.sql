-- +goose Up
ALTER TABLE agent_device_grants
    ADD COLUMN metadata JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE agent_toolbox_action_journal
    DROP CONSTRAINT agent_toolbox_action_journal_risk_check,
    ADD CONSTRAINT agent_toolbox_action_journal_risk_check
        CHECK(risk IN ('read','write','dangerous'));

-- +goose Down
ALTER TABLE agent_toolbox_action_journal
    DROP CONSTRAINT agent_toolbox_action_journal_risk_check,
    ADD CONSTRAINT agent_toolbox_action_journal_risk_check
        CHECK(risk IN ('write','dangerous'));
ALTER TABLE agent_device_grants DROP COLUMN IF EXISTS metadata;
