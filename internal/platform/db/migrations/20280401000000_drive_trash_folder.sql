-- +goose Up
-- A Space's trash is a folder in its drive, `.trash` at its top beside `.mistyignore`, for people
-- and agents alike. A delete moves the item there, where it is an ordinary item every member and
-- app sees; only the owner and moderators delete what is in it for good, and what went in 30 days
-- ago goes on its own. The item that went in keeps when and by whom (trashed_at, trashed_by) and
-- the folder and name it left (trashed_from, trashed_name), so it can be put back. Files in the
-- trash count against no storage limit: their contributions wait as 'trashed' until they are put
-- back or purged. What the old hidden trash held moves into each Space's .trash, keeping when it
-- went, and the hold that kept agents' clean-ups from expiring goes.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_drive_items ADD COLUMN trashed_from text;
ALTER TABLE space_drive_items ADD COLUMN trashed_name text;

ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_state_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_state_check
  CHECK (state IN ('active', 'recovery', 'released', 'trashed'));

-- +goose StatementBegin
CREATE FUNCTION pg_temp.misty_drive_free_name(sp text, parent text, wanted text, kind text) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
  base text := wanted;
  ext text := '';
  dot int;
  candidate text := wanted;
  n int := 1;
BEGIN
  IF kind = 'file' AND strpos(wanted, '.') > 0 THEN
    dot := length(wanted) - strpos(reverse(wanted), '.') + 1;
    IF dot > 1 THEN
      base := left(wanted, dot - 1);
      ext := substr(wanted, dot);
    END IF;
  END IF;
  WHILE EXISTS (SELECT 1 FROM space_drive_items
      WHERE space_id = sp AND COALESCE(parent_id, '') = COALESCE(parent, '') AND deleted_at IS NULL
      AND (trashed_at IS NULL OR parent_id = parent)
      AND misty_drive_name_fold(name) = misty_drive_name_fold(candidate)) LOOP
    n := n + 1;
    candidate := base || ' ' || n || ext;
    WHILE octet_length(candidate) > 255 AND base <> '' LOOP
      base := left(base, length(base) - 1);
      candidate := base || ' ' || n || ext;
    END LOOP;
  END LOOP;
  RETURN candidate;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
  s record;
  t record;
  bin text;
  owner text;
  next_seq bigint;
  freed bigint;
BEGIN
  FOR s IN SELECT DISTINCT space_id FROM space_drive_items
      WHERE deleted_at IS NULL AND (trashed_at IS NOT NULL
        OR (parent_id IS NULL AND kind = 'folder' AND misty_drive_name_fold(name) = misty_drive_name_fold('.trash'))) LOOP
    INSERT INTO space_drives(space_id) VALUES (s.space_id) ON CONFLICT DO NOTHING;
    bin := NULL;
    SELECT id INTO bin FROM space_drive_items
      WHERE space_id = s.space_id AND parent_id IS NULL AND kind = 'folder' AND trashed_at IS NULL AND deleted_at IS NULL
      AND misty_drive_name_fold(name) = misty_drive_name_fold('.trash') LIMIT 1;
    IF bin IS NULL THEN
      FOR t IN SELECT id, name FROM space_drive_items
          WHERE space_id = s.space_id AND parent_id IS NULL AND kind = 'file' AND trashed_at IS NULL AND deleted_at IS NULL
          AND misty_drive_name_fold(name) = misty_drive_name_fold('.trash') LOOP
        UPDATE space_drives SET drive_seq = drive_seq + 1 WHERE space_id = s.space_id RETURNING drive_seq INTO next_seq;
        UPDATE space_drive_items SET name = pg_temp.misty_drive_free_name(s.space_id, NULL, '.trash (renamed)', 'folder'),
          version = version + 1, seq = next_seq WHERE id = t.id;
      END LOOP;
      SELECT owner_user_id INTO owner FROM spaces WHERE id = s.space_id;
      bin := 'drv_' || gen_random_uuid();
      UPDATE space_drives SET drive_seq = drive_seq + 1 WHERE space_id = s.space_id RETURNING drive_seq INTO next_seq;
      INSERT INTO space_drive_items(id, space_id, parent_id, name, kind, seq, modified_by)
        VALUES (bin, s.space_id, NULL, '.trash', 'folder', next_seq, owner);
    END IF;
    FOR t IN SELECT id, parent_id, name, kind FROM space_drive_items
        WHERE space_id = s.space_id AND trashed_at IS NOT NULL AND deleted_at IS NULL AND parent_id IS DISTINCT FROM bin
        ORDER BY trashed_at, id LOOP
      UPDATE space_drives SET drive_seq = drive_seq + 1 WHERE space_id = s.space_id RETURNING drive_seq INTO next_seq;
      UPDATE space_drive_items SET parent_id = bin, name = pg_temp.misty_drive_free_name(s.space_id, bin, t.name, t.kind),
        trashed_from = t.parent_id, trashed_name = t.name, version = version + 1, seq = next_seq
        WHERE id = t.id;
    END LOOP;
    UPDATE space_drive_items SET trashed_at = NOW(), trashed_by = modified_by
      WHERE parent_id = bin AND trashed_at IS NULL AND deleted_at IS NULL;
    WITH RECURSIVE sub(id, depth) AS (
        SELECT bin, 0
        UNION ALL
        SELECT c.id, sub.depth + 1 FROM space_drive_items c JOIN sub ON c.parent_id = sub.id WHERE sub.depth < 4096),
      moved AS (
        UPDATE space_storage_contributions SET state = 'trashed', updated_at = NOW()
        WHERE space_id = s.space_id AND source_kind = 'drive' AND source_id IN (SELECT id FROM sub)
        AND state IN ('active', 'recovery') RETURNING logical_bytes)
    SELECT COALESCE(sum(logical_bytes), 0) INTO freed FROM moved;
    IF freed > 0 THEN
      UPDATE space_storage_usage SET used_bytes = GREATEST(0, used_bytes - freed), version = version + 1, updated_at = NOW()
        WHERE space_id = s.space_id;
    END IF;
  END LOOP;
  UPDATE space_drive_items SET trashed_at = NULL, trashed_by = NULL
    WHERE trashed_at IS NOT NULL AND (parent_id IS NULL OR parent_id NOT IN (
      SELECT id FROM space_drive_items WHERE parent_id IS NULL AND kind = 'folder' AND deleted_at IS NULL
      AND misty_drive_name_fold(name) = misty_drive_name_fold('.trash')));
END
$$;
-- +goose StatementEnd

DROP FUNCTION pg_temp.misty_drive_free_name(text, text, text, text);

DROP INDEX IF EXISTS space_drive_items_trash_idx;
ALTER TABLE space_drive_items DROP COLUMN trash_hold;
CREATE INDEX IF NOT EXISTS space_drive_items_trash_expiry_idx ON space_drive_items(trashed_at)
  WHERE trashed_at IS NOT NULL AND deleted_at IS NULL;
DROP INDEX IF EXISTS space_drive_items_name_idx;
CREATE UNIQUE INDEX space_drive_items_name_idx ON space_drive_items(space_id, COALESCE(parent_id, ''), lower(name))
  WHERE deleted_at IS NULL;
DROP INDEX IF EXISTS space_drive_items_name_fold_idx;
CREATE INDEX space_drive_items_name_fold_idx
  ON space_drive_items(space_id, COALESCE(parent_id, ''), misty_drive_name_fold(name))
  WHERE deleted_at IS NULL;
ALTER TABLE space_drive_items ADD CONSTRAINT space_drive_items_trashed_from_ck
  CHECK (trashed_at IS NOT NULL OR (trashed_from IS NULL AND trashed_name IS NULL));

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_drive_items DROP CONSTRAINT IF EXISTS space_drive_items_trashed_from_ck;
DROP INDEX IF EXISTS space_drive_items_name_fold_idx;
CREATE INDEX space_drive_items_name_fold_idx
  ON space_drive_items(space_id, COALESCE(parent_id, ''), misty_drive_name_fold(name))
  WHERE trashed_at IS NULL AND deleted_at IS NULL;
DROP INDEX IF EXISTS space_drive_items_name_idx;
CREATE UNIQUE INDEX space_drive_items_name_idx ON space_drive_items(space_id, COALESCE(parent_id, ''), lower(name))
  WHERE trashed_at IS NULL AND deleted_at IS NULL;
DROP INDEX IF EXISTS space_drive_items_trash_expiry_idx;
ALTER TABLE space_drive_items ADD COLUMN trash_hold boolean NOT NULL DEFAULT false;
ALTER TABLE space_drive_items ADD CONSTRAINT space_drive_items_trash_hold_ck CHECK (trashed_at IS NOT NULL OR NOT trash_hold);
CREATE INDEX space_drive_items_trash_idx ON space_drive_items(space_id, trashed_at DESC, id DESC)
  WHERE trashed_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX space_drive_items_trash_expiry_idx ON space_drive_items(trashed_at)
  WHERE trashed_at IS NOT NULL AND deleted_at IS NULL AND NOT trash_hold;
UPDATE space_storage_contributions SET state = 'active', updated_at = NOW() WHERE state = 'trashed';
ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_state_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_state_check
  CHECK (state IN ('active', 'recovery', 'released'));
ALTER TABLE space_drive_items DROP COLUMN IF EXISTS trashed_name;
ALTER TABLE space_drive_items DROP COLUMN IF EXISTS trashed_from;
