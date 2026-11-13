-- +goose Up
-- A development eval may give a run up to four hours of execution (plan_models.go,
-- evalExecutionLimitMS): Odysseys tasks are long on purpose, and the eval stops a run only
-- when it stops making progress. Every other run keeps the 30-minute default.
ALTER TABLE ai_invocations DROP CONSTRAINT IF EXISTS ai_invocations_execution_limit_ms_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_execution_limit_ms_check CHECK (execution_limit_ms >= 1 AND execution_limit_ms <= 14400000);

-- +goose Down
UPDATE ai_invocations SET execution_limit_ms = 1800000 WHERE execution_limit_ms > 1800000;
ALTER TABLE ai_invocations DROP CONSTRAINT IF EXISTS ai_invocations_execution_limit_ms_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_execution_limit_ms_check CHECK (execution_limit_ms >= 1 AND execution_limit_ms <= 1800000);
