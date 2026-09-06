-- +goose Up
-- Native task mutations retain workflow/assignment work until its native
-- consumer is assembled. Go task callbacks do not consume this queue.
CREATE TABLE native_task_effects (
    id BIGINT PRIMARY KEY REFERENCES space_events(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    actor_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES space_tasks(id) ON DELETE CASCADE,
    event_kind TEXT NOT NULL CHECK(event_kind IN ('created','updated','moved','archived')),
    task_version BIGINT NOT NULL,
    payload JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed','canceled')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_id UUID,
    lease_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error_code TEXT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(task_id,task_version,event_kind)
);
CREATE INDEX native_task_effects_due ON native_task_effects(available_at,id) WHERE state='pending';
ALTER TABLE native_task_effects ENABLE ROW LEVEL SECURITY;
ALTER TABLE native_task_effects FORCE ROW LEVEL SECURITY;
CREATE POLICY native_task_effects_service ON native_task_effects FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON native_task_effects TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve pending workflow effects during application rollback.
SELECT 1;
