-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Drawing image uploads use the same immutable R2 blob and quota system as
-- Library and note assets, but authorization follows the parent drawing.
ALTER TABLE space_library_uploads
    ADD COLUMN IF NOT EXISTS drawing_id TEXT
        REFERENCES space_drawings(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS drawing_file_id TEXT;

ALTER TABLE space_library_uploads
    DROP CONSTRAINT IF EXISTS space_library_uploads_purpose_check,
    DROP CONSTRAINT IF EXISTS space_library_uploads_note_purpose_ck;

ALTER TABLE space_library_uploads
    ADD CONSTRAINT space_library_uploads_purpose_check
        CHECK (purpose IN (
            'library',
            'attachment',
            'note_attachment',
            'drawing_attachment'
        )),
    ADD CONSTRAINT space_library_uploads_note_purpose_ck
        CHECK ((purpose = 'note_attachment') = (note_id IS NOT NULL)),
    ADD CONSTRAINT space_library_uploads_drawing_purpose_ck
        CHECK (
            (purpose = 'drawing_attachment') =
            (drawing_id IS NOT NULL AND drawing_file_id IS NOT NULL)
        ),
    ADD CONSTRAINT space_library_uploads_single_parent_ck
        CHECK (NOT (note_id IS NOT NULL AND drawing_id IS NOT NULL)),
    ADD CONSTRAINT space_library_uploads_drawing_file_id_ck
        CHECK (
            drawing_file_id IS NULL
            OR char_length(drawing_file_id) BETWEEN 1 AND 160
        );

-- Journal assets participate in the same storage accounting ledger as Library
-- items, without becoming browsable Library records.
ALTER TABLE space_storage_contributions
    DROP CONSTRAINT IF EXISTS space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_source_kind_check
        CHECK (source_kind IN (
            'attachment',
            'library_item',
            'import',
            'duplicate',
            'edit',
            'export',
            'note_asset',
            'drawing_asset'
        ));

CREATE INDEX IF NOT EXISTS space_library_uploads_drawing_idx
    ON space_library_uploads(drawing_id)
    WHERE drawing_id IS NOT NULL;

CREATE TABLE space_drawing_assets (
    id TEXT PRIMARY KEY,
    drawing_id TEXT NOT NULL
        REFERENCES space_drawings(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL
        REFERENCES library_files(id) ON DELETE RESTRICT,
    uploader_user_id TEXT NOT NULL
        REFERENCES users(id) ON DELETE RESTRICT,
    excalidraw_file_id TEXT NOT NULL
        CHECK (char_length(excalidraw_file_id) BETWEEN 1 AND 160),
    display_name TEXT NOT NULL
        CHECK (char_length(display_name) BETWEEN 1 AND 255),
    lifecycle_state TEXT NOT NULL DEFAULT 'ready'
        CHECK (lifecycle_state IN ('ready','unreferenced','deleting','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (drawing_id, excalidraw_file_id)
);

CREATE INDEX space_drawing_assets_drawing_idx
    ON space_drawing_assets(drawing_id, lifecycle_state);
CREATE INDEX space_drawing_assets_cleanup_idx
    ON space_drawing_assets(lifecycle_state, deleted_at)
    WHERE lifecycle_state <> 'ready';

ALTER TABLE space_drawing_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drawing_assets FORCE ROW LEVEL SECURITY;

CREATE POLICY space_drawing_assets_access_policy
    ON space_drawing_assets FOR ALL
    USING (
        misty_rls_is_service()
        OR EXISTS (
            SELECT 1
            FROM space_drawings d
            JOIN space_members m ON m.space_id=d.space_id
            WHERE d.id=drawing_id
              AND d.lifecycle_state='active'
              AND m.user_id=misty_rls_user_id()
        )
    )
    WITH CHECK (
        misty_rls_is_service()
        OR EXISTS (
            SELECT 1
            FROM space_drawings d
            JOIN space_members m ON m.space_id=d.space_id
            WHERE d.id=drawing_id
              AND d.lifecycle_state='active'
              AND m.user_id=misty_rls_user_id()
        )
    );

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE
            ON space_drawing_assets TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_drawing_assets;
DROP INDEX IF EXISTS space_library_uploads_drawing_idx;

ALTER TABLE space_library_uploads
    DROP CONSTRAINT IF EXISTS space_library_uploads_drawing_file_id_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_single_parent_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_drawing_purpose_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_note_purpose_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_purpose_check;

ALTER TABLE space_library_uploads
    DROP COLUMN IF EXISTS drawing_file_id,
    DROP COLUMN IF EXISTS drawing_id;

ALTER TABLE space_library_uploads
    ADD CONSTRAINT space_library_uploads_purpose_check
        CHECK (purpose IN ('library','attachment','note_attachment')),
    ADD CONSTRAINT space_library_uploads_note_purpose_ck
        CHECK ((purpose = 'note_attachment') = (note_id IS NOT NULL));

ALTER TABLE space_storage_contributions
    DROP CONSTRAINT IF EXISTS space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_source_kind_check
        CHECK (source_kind IN (
            'attachment',
            'library_item',
            'import',
            'duplicate',
            'edit',
            'export'
        ));
-- +goose StatementEnd
