-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Notes are private to their creator by default. The creator is the only
-- permission administrator; a Space owner has no override. PostgreSQL is
-- authoritative for identity, metadata projections, permissions, and lifecycle,
-- while the Durable Object owns the collaborative Yjs document itself.
CREATE TABLE space_notes (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    creator_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Projections are maintained from the CRDT by the collaboration service.
    -- They exist for list and search only; the full document is never stored.
    title_projection TEXT NOT NULL DEFAULT '',
    plain_text_projection TEXT NOT NULL DEFAULT '' CHECK (char_length(plain_text_projection) <= 100000),
    shared_tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active','archived_creator_left','deleting')),
    archived_at TIMESTAMPTZ,
    purge_after TIMESTAMPTZ,
    collaboration_revision BIGINT NOT NULL DEFAULT 0,
    acl_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- An archived note must carry both timestamps so the retention worker can
    -- always find its deadline.
    CHECK (lifecycle_state <> 'archived_creator_left' OR (archived_at IS NOT NULL AND purge_after IS NOT NULL))
);

CREATE INDEX space_notes_space_recent_idx
    ON space_notes(space_id, lifecycle_state, updated_at DESC);
CREATE INDEX space_notes_creator_idx
    ON space_notes(creator_user_id, lifecycle_state);
CREATE INDEX space_notes_purge_idx
    ON space_notes(purge_after) WHERE purge_after IS NOT NULL;
CREATE INDEX space_notes_search_idx ON space_notes USING GIN (
    (setweight(to_tsvector('simple'::regconfig, COALESCE(title_projection, '')), 'A') ||
     setweight(to_tsvector('simple'::regconfig, COALESCE(shared_tags::text, '')), 'B') ||
     setweight(to_tsvector('simple'::regconfig, COALESCE(plain_text_projection, '')), 'C'))
);

-- Only the creator may grant access, and only to a current member of the
-- note's Space. The creator never has a row here: their access is implicit.
CREATE TABLE space_note_permissions (
    note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('viewer','editor')),
    granted_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (note_id, user_id)
);

CREATE INDEX space_note_permissions_user_idx ON space_note_permissions(user_id);

-- The creator's implicit access must never be shadowed by an explicit row, and
-- the grantor must be the creator. Both are enforced in the database so no
-- application path can bypass them.
CREATE OR REPLACE FUNCTION misty_note_permission_guard() RETURNS TRIGGER AS $guard$
DECLARE
    note_creator TEXT;
    note_space TEXT;
BEGIN
    SELECT creator_user_id, space_id INTO note_creator, note_space
    FROM space_notes WHERE id = NEW.note_id;
    IF note_creator IS NULL THEN
        RAISE EXCEPTION 'note % does not exist', NEW.note_id;
    END IF;
    IF NEW.user_id = note_creator THEN
        RAISE EXCEPTION 'the note creator has implicit access and cannot hold a permission row';
    END IF;
    IF NEW.granted_by <> note_creator THEN
        RAISE EXCEPTION 'only the note creator may grant note access';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM space_members WHERE space_id = note_space AND user_id = NEW.user_id) THEN
        RAISE EXCEPTION 'note access may only be granted to a current member of the note Space';
    END IF;
    RETURN NEW;
END;
$guard$ LANGUAGE plpgsql;

CREATE TRIGGER space_note_permissions_guard
    BEFORE INSERT OR UPDATE ON space_note_permissions
    FOR EACH ROW EXECUTE FUNCTION misty_note_permission_guard();

-- Per-user UI state. Favorites never affect access.
CREATE TABLE space_note_preferences (
    note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_favorite BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (note_id, user_id)
);

-- Note assets inherit access exclusively from the parent note.
CREATE TABLE space_note_assets (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE RESTRICT,
    uploader_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    lifecycle_state TEXT NOT NULL DEFAULT 'ready'
        CHECK (lifecycle_state IN ('ready','unreferenced','deleting','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX space_note_assets_note_idx ON space_note_assets(note_id, lifecycle_state);
CREATE INDEX space_note_assets_cleanup_idx
    ON space_note_assets(lifecycle_state, deleted_at) WHERE lifecycle_state <> 'ready';

-- Retryable outbox for room ACL, disconnect, and purge commands sent to the
-- collaboration service. A failed control delivery must never be lost.
CREATE TABLE space_note_control_outbox (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL REFERENCES space_notes(id) ON DELETE CASCADE,
    command TEXT NOT NULL CHECK (command IN ('acl','disconnect','purge')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NOT NULL DEFAULT '',
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_note_control_outbox_pending_idx
    ON space_note_control_outbox(next_attempt_at) WHERE delivered_at IS NULL;

ALTER TABLE space_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_notes FORCE ROW LEVEL SECURITY;
ALTER TABLE space_note_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_note_permissions FORCE ROW LEVEL SECURITY;
ALTER TABLE space_note_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_note_preferences FORCE ROW LEVEL SECURITY;
ALTER TABLE space_note_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_note_assets FORCE ROW LEVEL SECURITY;
ALTER TABLE space_note_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_note_control_outbox FORCE ROW LEVEL SECURITY;

-- A note is visible to its creator, or to a current Space member holding an
-- active permission row. Space ownership grants nothing: there is deliberately
-- no owner clause here. An archived note is visible to nobody.
CREATE POLICY space_notes_access_policy ON space_notes FOR ALL
    USING (misty_rls_is_service() OR (
        lifecycle_state = 'active' AND (
            creator_user_id = misty_rls_user_id()
            OR EXISTS (
                SELECT 1 FROM space_note_permissions p
                JOIN space_members m ON m.space_id = space_notes.space_id AND m.user_id = p.user_id
                WHERE p.note_id = space_notes.id AND p.user_id = misty_rls_user_id()
            )
        )
    ))
    WITH CHECK (misty_rls_is_service() OR creator_user_id = misty_rls_user_id());

-- Only the creator may read or modify the full grant set. A recipient can see
-- their own row so the client can render its own effective role.
CREATE POLICY space_note_permissions_policy ON space_note_permissions FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id() OR EXISTS (
        SELECT 1 FROM space_notes n WHERE n.id = note_id AND n.creator_user_id = misty_rls_user_id()))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM space_notes n WHERE n.id = note_id AND n.creator_user_id = misty_rls_user_id()));

CREATE POLICY space_note_preferences_policy ON space_note_preferences FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

CREATE POLICY space_note_assets_policy ON space_note_assets FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM space_notes n WHERE n.id = note_id))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM space_notes n WHERE n.id = note_id));

-- The outbox carries service commands only; no user session may read it.
CREATE POLICY space_note_control_outbox_policy ON space_note_control_outbox FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_notes,space_note_permissions,
            space_note_preferences,space_note_assets,space_note_control_outbox TO misty_app;
        GRANT EXECUTE ON FUNCTION misty_note_permission_guard() TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_note_control_outbox,space_note_assets,space_note_preferences,space_note_permissions;
DROP TRIGGER IF EXISTS space_note_permissions_guard ON space_note_permissions;
DROP FUNCTION IF EXISTS misty_note_permission_guard();
DROP TABLE IF EXISTS space_notes;
-- +goose StatementEnd
