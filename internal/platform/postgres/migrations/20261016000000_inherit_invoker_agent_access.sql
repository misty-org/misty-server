-- +goose Up
-- Existing and new Agents start with every broad surface enabled. Effective
-- authorization is still intersected with the invoking human (or schedule
-- owner), Space membership, connection scopes, and normal confirmation rules.
UPDATE personal_agents
SET context_permissions = '{"space_chat":true,"library":true,"notes":true,"task_notes":true,"tasks":true,"members":true}'::jsonb,
    tool_permissions = jsonb_set(
      jsonb_set(COALESCE(tool_permissions, '{}'::jsonb), '{mode}', '"inherit_invoker"'::jsonb, TRUE),
      '{disabled_surfaces}', '[]'::jsonb, TRUE
    ) || '{"read":true,"write":true}'::jsonb;

UPDATE personal_agent_versions
SET context_permissions = '{"space_chat":true,"library":true,"notes":true,"task_notes":true,"tasks":true,"members":true}'::jsonb,
    tool_permissions = jsonb_set(
      jsonb_set(COALESCE(tool_permissions, '{}'::jsonb), '{mode}', '"inherit_invoker"'::jsonb, TRUE),
      '{disabled_surfaces}', '[]'::jsonb, TRUE
    ) || '{"read":true,"write":true}'::jsonb;

-- +goose Down
UPDATE personal_agents
SET tool_permissions = (tool_permissions - 'mode' - 'disabled_surfaces');

UPDATE personal_agent_versions
SET tool_permissions = (tool_permissions - 'mode' - 'disabled_surfaces');
