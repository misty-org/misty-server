-- +goose Up
-- A Space's channel categories, as Discord's: named groups its managers (channels.manage) make,
-- set in order by position. Each channel sits in at most one category, or in none, at the top of
-- the list, in order by its own position within that group. Deleting a category moves its
-- channels out to the top rather than deleting them.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_channel_categories (
    id text PRIMARY KEY CHECK (char_length(id) BETWEEN 1 AND 128),
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    position integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_channel_categories_order ON space_channel_categories(space_id, position, created_at);

ALTER TABLE space_conversations
  ADD COLUMN category_id text REFERENCES space_channel_categories(id) ON DELETE SET NULL,
  ADD COLUMN position integer NOT NULL DEFAULT 0,
  ADD CONSTRAINT space_conversations_category_kind_check CHECK (category_id IS NULL OR kind = 'standard');
CREATE INDEX space_conversations_category ON space_conversations(category_id) WHERE category_id IS NOT NULL;

UPDATE space_conversations c SET position = ordered.n
FROM (SELECT id, (row_number() OVER (PARTITION BY space_id ORDER BY created_at, id))::integer - 1 AS n
      FROM space_conversations WHERE kind = 'standard') ordered
WHERE c.id = ordered.id;

ALTER TABLE space_channel_categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_channel_categories FORCE ROW LEVEL SECURITY;
CREATE POLICY space_channel_categories_members ON space_channel_categories
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_channel_categories TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
ALTER TABLE space_conversations
  DROP CONSTRAINT IF EXISTS space_conversations_category_kind_check,
  DROP COLUMN IF EXISTS position,
  DROP COLUMN IF EXISTS category_id;
DROP TABLE IF EXISTS space_channel_categories;
