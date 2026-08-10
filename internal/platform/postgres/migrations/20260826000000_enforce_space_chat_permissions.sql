-- +goose Up
SET LOCAL app.rls_mode = 'service';

-- Existing Space members could already read and post messages. Preserve that
-- behavior while making both capabilities explicit and independently
-- revocable through the Space permission model.
UPDATE space_roles
SET permissions = permissions
        || CASE WHEN permissions ? 'messages.read' THEN '[]'::jsonb ELSE '["messages.read"]'::jsonb END
        || CASE WHEN permissions ? 'messages.write' THEN '[]'::jsonb ELSE '["messages.write"]'::jsonb END,
    version = version + 1,
    updated_at = NOW()
WHERE is_everyone
  AND NOT (permissions ? 'messages.read' AND permissions ? 'messages.write');

-- +goose Down
SET LOCAL app.rls_mode = 'service';

UPDATE space_roles
SET permissions = permissions - 'messages.write',
    version = version + 1,
    updated_at = NOW()
WHERE is_everyone;
