-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE collab_documents (
  room_id text PRIMARY KEY REFERENCES collab_rooms(id) ON DELETE CASCADE,
  state bytea NOT NULL,
  byte_size integer NOT NULL CHECK (byte_size BETWEEN 0 AND 8388608),
  checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
  saved_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE collab_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE collab_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY collab_documents_service ON collab_documents
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON collab_documents TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS collab_documents;
