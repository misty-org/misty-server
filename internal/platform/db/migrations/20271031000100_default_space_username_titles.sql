-- +goose Up
-- Personal Spaces made before the username-only change carry a title built from the old display
-- name ("Matthew's Space"). Retitle every default Space that still has an auto-style title
-- ("<someone>'s Space", in any case) from its owner's username. A Space renamed to anything
-- else is left alone.
UPDATE spaces s
SET name = u.username || '''s Space'
FROM users u
WHERE s.owner_user_id = u.id
  AND s.is_default
  AND NULLIF(btrim(u.username), '') IS NOT NULL
  AND s.name ILIKE '%''s space'
  AND s.name <> u.username || '''s Space';

-- +goose Down
-- The previous titles are not recovered.
SELECT 1;
