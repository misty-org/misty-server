-- +goose Up
-- +goose StatementBegin
ALTER TABLE space_library_intelligence_policies
    ADD COLUMN ocr_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN ai_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN semantic_search_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE space_library_search_documents (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE CASCADE,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE CASCADE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    search_text TEXT NOT NULL DEFAULT '',
    search_tsv TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig,search_text||' '||metadata::text)) STORED,
    embedding VECTOR(768),
    embedding_model TEXT,
    embedding_version INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'processing' CHECK (state IN ('processing','ready','failed')),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,space_library_item_id)
);
CREATE INDEX space_library_search_documents_lexical_idx ON space_library_search_documents USING GIN(search_tsv);
CREATE INDEX space_library_search_documents_semantic_idx ON space_library_search_documents USING HNSW(embedding vector_cosine_ops) WHERE embedding IS NOT NULL AND state='ready';

ALTER TABLE space_library_search_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_search_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_search_documents_policy ON space_library_search_documents FOR SELECT
USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_library_search_documents_service_write ON space_library_search_documents FOR ALL
USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_library_search_documents TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_library_search_documents;
ALTER TABLE space_library_intelligence_policies
    DROP COLUMN IF EXISTS semantic_search_enabled,
    DROP COLUMN IF EXISTS ai_enabled,
    DROP COLUMN IF EXISTS ocr_enabled;
-- +goose StatementEnd
