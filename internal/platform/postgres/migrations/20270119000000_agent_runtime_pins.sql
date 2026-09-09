-- +goose Up
-- The current beta adapter is pinned at admission. Older runs use that same
-- implementation; their endpoint is captured on their first post-migration delivery.
ALTER TABLE space_runs ADD COLUMN runtime_adapter_version TEXT NOT NULL DEFAULT 'vercel-workflow/1', ADD COLUMN runtime_endpoint TEXT, ADD COLUMN runtime_callback_endpoint TEXT;
ALTER TABLE ai_invocations ADD COLUMN runtime_adapter_version TEXT NOT NULL DEFAULT 'vercel-workflow/1', ADD COLUMN runtime_endpoint TEXT, ADD COLUMN runtime_callback_endpoint TEXT;
-- +goose StatementBegin
CREATE FUNCTION preserve_agent_runtime_pin() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.runtime_adapter_version IS DISTINCT FROM OLD.runtime_adapter_version
    OR (OLD.runtime_endpoint IS NOT NULL AND NEW.runtime_endpoint IS DISTINCT FROM OLD.runtime_endpoint)
    OR (OLD.runtime_callback_endpoint IS NOT NULL AND NEW.runtime_callback_endpoint IS DISTINCT FROM OLD.runtime_callback_endpoint) THEN
    RAISE EXCEPTION 'agent_runtime_pin_immutable';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_agent_runtime_pin BEFORE UPDATE ON space_runs FOR EACH ROW EXECUTE FUNCTION preserve_agent_runtime_pin();
CREATE TRIGGER preserve_agent_runtime_pin BEFORE UPDATE ON ai_invocations FOR EACH ROW EXECUTE FUNCTION preserve_agent_runtime_pin();

-- +goose Down
DROP TRIGGER preserve_agent_runtime_pin ON space_runs;
DROP TRIGGER preserve_agent_runtime_pin ON ai_invocations;
DROP FUNCTION preserve_agent_runtime_pin();
ALTER TABLE space_runs DROP COLUMN runtime_adapter_version, DROP COLUMN runtime_endpoint, DROP COLUMN runtime_callback_endpoint;
ALTER TABLE ai_invocations DROP COLUMN runtime_adapter_version, DROP COLUMN runtime_endpoint, DROP COLUMN runtime_callback_endpoint;
