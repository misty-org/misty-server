-- +goose Up
-- Get started steps the account has done that leave no other trace: asking Misty
-- with a web page in front, and running a workflow (by hand or on a schedule).
-- The server records each the first time it accepts such a request.
CREATE TABLE agent_onboarding_steps (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    step text NOT NULL CHECK (step IN ('page_ask', 'workflow_run')),
    completed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, step)
);

ALTER TABLE agent_onboarding_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_onboarding_steps FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_onboarding_steps_owner ON agent_onboarding_steps
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- Every device's Get started card follows one content-free account topic.
CREATE TRIGGER agent_onboarding_step_account_notify
AFTER INSERT ON agent_onboarding_steps
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('onboarding', 'user_id');

-- +goose Down
DROP TRIGGER IF EXISTS agent_onboarding_step_account_notify ON agent_onboarding_steps;
DROP TABLE IF EXISTS agent_onboarding_steps;
