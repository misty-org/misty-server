-- +goose Up
-- Activity replaced the Space inbox; nothing reads or writes it. Its rows held
-- message previews, so the table goes rather than lingering.
DROP TABLE space_inbox_items;

-- +goose Down
CREATE TABLE space_inbox_items (
    id bigserial PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('unread','mention','agent','approval','workflow')),
    message_id text REFERENCES space_messages(id) ON DELETE CASCADE,
    event_id bigint REFERENCES space_events(id) ON DELETE CASCADE,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_inbox_user_idx ON space_inbox_items (user_id, kind, id DESC);
ALTER TABLE space_inbox_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_inbox_items FORCE ROW LEVEL SECURITY;
CREATE POLICY space_inbox_user_policy ON space_inbox_items
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
GRANT SELECT, USAGE ON SEQUENCE space_inbox_items_id_seq TO misty_app;
