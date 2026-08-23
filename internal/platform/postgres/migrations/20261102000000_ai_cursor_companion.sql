-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE ai_invocations DROP CONSTRAINT IF EXISTS ai_invocations_mode_check;
ALTER TABLE ai_invocations
    ADD CONSTRAINT ai_invocations_mode_check CHECK(mode IN ('quick','drawer','companion'));

ALTER TABLE ai_artifacts DROP CONSTRAINT IF EXISTS ai_artifacts_approval_policy_check;
ALTER TABLE ai_artifacts
    ADD CONSTRAINT ai_artifacts_approval_policy_check
        CHECK(approval_policy IN ('none','auto_apply_with_undo','visible_apply','confirm','always_confirm'));

ALTER TABLE ai_user_settings
    ADD COLUMN cursor_companion_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN active_companion_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL;

ALTER TABLE agent_conversations
    ADD COLUMN conversation_kind TEXT NOT NULL DEFAULT 'misty'
        CHECK(conversation_kind IN ('misty','companion_task')),
    ADD COLUMN origin_surface TEXT NOT NULL DEFAULT '',
    ADD COLUMN origin_href TEXT NOT NULL DEFAULT '',
    ADD COLUMN privacy_boundary TEXT NOT NULL DEFAULT '';

ALTER TABLE space_runs
    ADD COLUMN source_agent_conversation_id TEXT REFERENCES agent_conversations(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE space_runs DROP COLUMN IF EXISTS source_agent_conversation_id;
ALTER TABLE agent_conversations
    DROP COLUMN IF EXISTS privacy_boundary,
    DROP COLUMN IF EXISTS origin_href,
    DROP COLUMN IF EXISTS origin_surface,
    DROP COLUMN IF EXISTS conversation_kind;
ALTER TABLE ai_user_settings
    DROP COLUMN IF EXISTS active_companion_agent_id,
    DROP COLUMN IF EXISTS cursor_companion_enabled;
ALTER TABLE ai_invocations DROP CONSTRAINT IF EXISTS ai_invocations_mode_check;
ALTER TABLE ai_invocations
    ADD CONSTRAINT ai_invocations_mode_check CHECK(mode IN ('quick','drawer'));
ALTER TABLE ai_artifacts DROP CONSTRAINT IF EXISTS ai_artifacts_approval_policy_check;
ALTER TABLE ai_artifacts
    ADD CONSTRAINT ai_artifacts_approval_policy_check
        CHECK(approval_policy IN ('none','visible_apply','confirm','always_confirm'));
-- +goose StatementEnd
