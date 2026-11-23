-- +goose Up
-- Two drive names clash when a Mac's case-blind drive would see them as one: compared after
-- decomposing them (NFD), lowering their case and folding the few letters whose lower case is
-- not their folded form (ß, final sigma, the Latin ligatures). The desktop app folds names the
-- same way (space_drive/src/names.rs). An in-use mark a desktop holds over its realtime
-- connection lasts as long as that connection and is cleared when it ends, rather than
-- expiring on a timer. held_by names the server process holding it (its session advisory
-- lock), so a process that starts clears only the holds of processes that are gone.
SELECT set_config('app.rls_mode', 'service', true);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_drive_name_fold(name text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT replace(replace(replace(replace(replace(replace(replace(replace(replace(
    lower(normalize(name, NFD)),
    'ß', 'ss'), 'ς', 'σ'), 'ﬀ', 'ff'), 'ﬁ', 'fi'), 'ﬂ', 'fl'), 'ﬃ', 'ffi'), 'ﬄ', 'ffl'),
    'ﬅ', 'st'), 'ﬆ', 'st')
$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS space_drive_items_name_fold_idx
  ON space_drive_items(space_id, COALESCE(parent_id, ''), misty_drive_name_fold(name))
  WHERE trashed_at IS NULL AND deleted_at IS NULL;

ALTER TABLE space_drive_in_use ADD COLUMN IF NOT EXISTS connection_held boolean NOT NULL DEFAULT false;
ALTER TABLE space_drive_in_use ADD COLUMN IF NOT EXISTS held_by integer;

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DELETE FROM space_drive_in_use WHERE connection_held;
ALTER TABLE space_drive_in_use DROP COLUMN IF EXISTS held_by;
ALTER TABLE space_drive_in_use DROP COLUMN IF EXISTS connection_held;
DROP INDEX IF EXISTS space_drive_items_name_fold_idx;
DROP FUNCTION IF EXISTS misty_drive_name_fold(text);
