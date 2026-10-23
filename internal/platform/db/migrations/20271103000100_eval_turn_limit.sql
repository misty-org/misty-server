-- +goose Up
-- A development eval may give a run up to 200 model turns (plan_models.go, evalTurnLimit), as
-- the Odysseys leaders run with 100 to 200 steps (docs/agents/harness.md, change 6).
-- Plans keep their own limits, all well below.
ALTER TABLE ai_invocations DROP CONSTRAINT IF EXISTS ai_invocations_model_turn_limit_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_model_turn_limit_check CHECK (model_turn_limit >= 1 AND model_turn_limit <= 200);

-- +goose Down
UPDATE ai_invocations SET model_turn_limit = 120 WHERE model_turn_limit > 120;
ALTER TABLE ai_invocations DROP CONSTRAINT IF EXISTS ai_invocations_model_turn_limit_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_model_turn_limit_check CHECK (model_turn_limit >= 1 AND model_turn_limit <= 120);
