-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

WITH defaults(app_id, version, permission_version, scopes, pin_rank) AS (
    VALUES
      ('inbox','1.0.0',2,'["spaces.read","activity.read","activity.write","messages.read","mail.read","mail.write","connections.read","connections.write","ai.read","ai.write"]'::jsonb,1024::bigint),
      ('chat','1.0.0',2,'["spaces.read","messages.read","messages.write","connections.read","connections.write","ai.read","ai.write"]'::jsonb,2048::bigint),
      ('journal','1.0.0',2,'["spaces.read","notes.read","notes.write","drawings.read","drawings.write","profile.read","connections.read","connections.write","ai.read","ai.write"]'::jsonb,3072::bigint),
      ('files','1.0.0',2,'["spaces.read","files.read","files.write","profile.read","connections.read","connections.write","media-search.read","media-search.write","search.read"]'::jsonb,4096::bigint),
      ('agents','1.0.0',2,'["spaces.read","agents.read","agents.write","profile.read","ai.read","ai.write","mcp.read","mcp.write","automations.read","automations.write","devices.read","devices.write"]'::jsonb,5120::bigint)
)
INSERT INTO user_app_installations
    (user_id,app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank)
SELECT users.id,defaults.app_id,'installed',defaults.version,defaults.permission_version,
       defaults.scopes,TRUE,defaults.pin_rank
FROM users CROSS JOIN defaults
WHERE users.lifecycle_state='active'
ON CONFLICT(user_id,app_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Acquisitions may have been changed by the user after this migration. A
-- rollback must not silently remove their Apps.
SELECT 1;
-- +goose StatementEnd
