-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- A note_attachment upload is authorized against its parent note rather than a
-- Space permission, and finalization must create a note asset instead of a
-- Library item. Both need the note identity to survive from initiation to
-- finalization, which may be a separate request minutes later.
ALTER TABLE space_library_uploads ADD COLUMN IF NOT EXISTS note_id TEXT REFERENCES space_notes(id) ON DELETE CASCADE;

-- The original purpose constraint predates note attachments and allows only
-- the two Space-authorized purposes.
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads
    ADD CONSTRAINT space_library_uploads_purpose_check
    CHECK (purpose IN ('library','attachment','note_attachment'));

-- The purpose and the note reference must agree in both directions: a note
-- upload without a note would finalize into nothing, and a Library upload
-- carrying a note id would be authorized against the wrong resource.
ALTER TABLE space_library_uploads
    ADD CONSTRAINT space_library_uploads_note_purpose_ck
    CHECK ((purpose = 'note_attachment') = (note_id IS NOT NULL));

CREATE INDEX IF NOT EXISTS space_library_uploads_note_idx
    ON space_library_uploads(note_id) WHERE note_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_library_uploads_note_idx;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_note_purpose_ck;
ALTER TABLE space_library_uploads DROP COLUMN IF EXISTS note_id;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads
    ADD CONSTRAINT space_library_uploads_purpose_check
    CHECK (purpose IN ('library','attachment'));
-- +goose StatementEnd
