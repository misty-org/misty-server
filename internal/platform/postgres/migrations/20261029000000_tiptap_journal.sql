-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Backlinks are a server projection of misty-note:// links in the Yjs body.
ALTER TABLE space_notes
    ADD COLUMN markdown_projection TEXT NOT NULL DEFAULT ''
    CHECK (char_length(markdown_projection) <= 100000);

CREATE TABLE space_note_links (
    source_note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    target_note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_note_id, target_note_id),
    CHECK (source_note_id <> target_note_id)
);
CREATE INDEX space_note_links_target_idx ON space_note_links(target_note_id, created_at DESC);
ALTER TABLE space_note_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_note_links FORCE ROW LEVEL SECURITY;
CREATE POLICY space_note_links_policy ON space_note_links FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM space_notes n WHERE n.id=source_note_id))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM space_notes n WHERE n.id=source_note_id));

-- Journal favorites were never part of document semantics and are removed by
-- the TipTap redesign.
DROP TABLE IF EXISTS space_note_preferences;

ALTER TABLE space_notes DROP CONSTRAINT IF EXISTS space_notes_lifecycle_state_check;
ALTER TABLE space_notes ADD CONSTRAINT space_notes_lifecycle_state_check
    CHECK (lifecycle_state IN ('active','archived','archived_creator_left','deleting'));

-- The approved BlockNote -> TipTap rollout starts native Journal clean. Notes
-- disappear immediately, while rooms and files stay addressable until their
-- retryable cleanup workers have completed.
UPDATE space_note_assets
SET lifecycle_state='deleting', deleted_at=COALESCE(deleted_at,NOW())
WHERE lifecycle_state IN ('ready','unreferenced');

UPDATE space_notes
SET lifecycle_state='deleting', acl_version=acl_version+1, updated_at=NOW()
WHERE lifecycle_state IN ('active','archived','archived_creator_left');

INSERT INTO space_note_control_outbox(id,note_id,command,payload)
SELECT 'notectl_tiptap_reset_' || md5(n.id),n.id,'purge','{}'::jsonb
FROM space_notes n
WHERE n.lifecycle_state='deleting'
  AND NOT EXISTS (
      SELECT 1 FROM space_note_control_outbox o
      WHERE o.note_id=n.id AND o.command='purge' AND o.delivered_at IS NULL)
ON CONFLICT (id) DO NOTHING;

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_note_links TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_note_links;
ALTER TABLE space_notes DROP COLUMN IF EXISTS markdown_projection;
CREATE TABLE IF NOT EXISTS space_note_preferences (
    note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_favorite BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (note_id,user_id)
);
-- The destructive native-note reset is intentionally not reversible.
-- +goose StatementEnd
