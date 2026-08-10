-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- PostgreSQL owns drawing identity, membership authorization, and lifecycle.
-- The collaborative Excalidraw scene itself is stored in the drawing's
-- Durable Object as a Yjs document.
CREATE TABLE space_drawings (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    creator_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title TEXT NOT NULL DEFAULT 'Untitled drawing'
        CHECK (char_length(title) BETWEEN 1 AND 200),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active','deleting')),
    collaboration_revision BIGINT NOT NULL DEFAULT 0,
    acl_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_drawings_space_recent_idx
    ON space_drawings(space_id, lifecycle_state, updated_at DESC);
CREATE INDEX space_drawings_creator_idx
    ON space_drawings(creator_user_id, lifecycle_state);

-- Room deletion is delivered asynchronously. This prevents an unavailable
-- collaboration Worker from rolling back a successful authorization change.
CREATE TABLE space_drawing_control_outbox (
    id TEXT PRIMARY KEY,
    drawing_id TEXT NOT NULL REFERENCES space_drawings(id) ON DELETE CASCADE,
    command TEXT NOT NULL CHECK (command IN ('acl','disconnect','purge')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NOT NULL DEFAULT '',
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_drawing_control_outbox_pending_idx
    ON space_drawing_control_outbox(next_attempt_at)
    WHERE delivered_at IS NULL;

ALTER TABLE space_drawings ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drawings FORCE ROW LEVEL SECURITY;
ALTER TABLE space_drawing_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drawing_control_outbox FORCE ROW LEVEL SECURITY;

-- Drawings are Space-wide collaborative documents. Current members may read
-- and edit active drawings; only service code may operate on deleting rows.
CREATE POLICY space_drawings_access_policy ON space_drawings FOR ALL
    USING (
        misty_rls_is_service()
        OR (
            lifecycle_state = 'active'
            AND EXISTS (
                SELECT 1
                FROM space_members m
                WHERE m.space_id = space_drawings.space_id
                  AND m.user_id = misty_rls_user_id()
            )
        )
    )
    WITH CHECK (
        misty_rls_is_service()
        OR (
            creator_user_id = misty_rls_user_id()
            AND EXISTS (
                SELECT 1
                FROM space_members m
                WHERE m.space_id = space_drawings.space_id
                  AND m.user_id = misty_rls_user_id()
            )
        )
    );

CREATE POLICY space_drawing_control_outbox_policy
    ON space_drawing_control_outbox FOR ALL
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE
            ON space_drawings,space_drawing_control_outbox TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_drawing_control_outbox;
DROP TABLE IF EXISTS space_drawings;
-- +goose StatementEnd
