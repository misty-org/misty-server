-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
-- Existing admissions keep their pinned worker's policy. Do not invent elapsed
-- time for work admitted before the clock existed.
ALTER TABLE space_runs ADD COLUMN execution_budget_version INTEGER NOT NULL DEFAULT 0 CHECK(execution_budget_version IN (0,1));
ALTER TABLE space_runs ALTER COLUMN execution_budget_version SET DEFAULT 1;
ALTER TABLE space_runs ADD COLUMN execution_limit_ms BIGINT NOT NULL DEFAULT 1800000 CHECK(execution_limit_ms BETWEEN 1 AND 1800000);
ALTER TABLE space_runs ADD COLUMN execution_consumed_ms BIGINT NOT NULL DEFAULT 0 CHECK(execution_consumed_ms>=0);
ALTER TABLE space_runs ADD COLUMN execution_active_at TIMESTAMPTZ;
ALTER TABLE ai_invocations ADD COLUMN execution_budget_version INTEGER NOT NULL DEFAULT 0 CHECK(execution_budget_version IN (0,1));
ALTER TABLE ai_invocations ALTER COLUMN execution_budget_version SET DEFAULT 1;
ALTER TABLE ai_invocations ADD COLUMN execution_limit_ms BIGINT NOT NULL DEFAULT 1800000 CHECK(execution_limit_ms BETWEEN 1 AND 1800000);
ALTER TABLE ai_invocations ADD COLUMN execution_consumed_ms BIGINT NOT NULL DEFAULT 0 CHECK(execution_consumed_ms>=0);
ALTER TABLE ai_invocations ADD COLUMN execution_active_at TIMESTAMPTZ;

CREATE FUNCTION misty_pause_agent_execution_clock() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- Pausing is part of the same transaction as every wait/terminal transition,
  -- including transitions made by older services. Starting belongs to operation
  -- admission, so queued starts and undelivered resumes do not consume time.
  IF NEW.state<>'running' AND OLD.execution_active_at IS NOT NULL THEN
    NEW.execution_consumed_ms := LEAST(OLD.execution_limit_ms, OLD.execution_consumed_ms +
      GREATEST(0,CEIL(EXTRACT(EPOCH FROM (clock_timestamp()-OLD.execution_active_at))*1000)::bigint));
    NEW.execution_active_at := NULL;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER pause_execution_clock BEFORE UPDATE OF state ON space_runs
FOR EACH ROW EXECUTE FUNCTION misty_pause_agent_execution_clock();
CREATE TRIGGER pause_execution_clock BEFORE UPDATE OF state ON ai_invocations
FOR EACH ROW EXECUTE FUNCTION misty_pause_agent_execution_clock();
-- +goose StatementEnd
-- +goose Down
-- Retain consumed time and paused clocks when rolling workers back.
SELECT 1;
