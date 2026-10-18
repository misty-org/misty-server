-- +goose Up
-- Memory: what the person read, heard and recorded, kept for them alone
-- (docs/design/voice-memory/BRIEF.md). Events are owner-only and never
-- shared through a Space. A revisit of a page that barely changed points at
-- the visit that holds its text (same_as) instead of storing it again.
CREATE TABLE memory_events (
    id uuid PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('page', 'voice', 'recording', 'note')),
    device_id text NOT NULL DEFAULT '' CHECK (char_length(device_id) <= 200),
    url text NOT NULL DEFAULT '' CHECK (char_length(url) <= 2048),
    site text NOT NULL DEFAULT '' CHECK (char_length(site) <= 255),
    title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 300),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
    simhash text NOT NULL DEFAULT '' CHECK (simhash = '' OR simhash ~ '^[0-9a-f]{16}$'),
    same_as uuid REFERENCES memory_events(id) ON DELETE SET NULL,
    ref_id text NOT NULL DEFAULT '' CHECK (char_length(ref_id) <= 200),
    dwell_ms bigint NOT NULL DEFAULT 0 CHECK (dwell_ms >= 0),
    occurred_at timestamptz NOT NULL,
    indexed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_events_owner_time ON memory_events (owner_user_id, occurred_at DESC);
CREATE INDEX memory_events_owner_url ON memory_events (owner_user_id, url, occurred_at DESC) WHERE url <> '';
CREATE INDEX memory_events_owner_site ON memory_events (owner_user_id, site);
CREATE INDEX memory_events_body_search ON memory_events
    USING gin (to_tsvector('simple', title || ' ' || body));

-- Events waiting for an embedding. Past the per-person cap the recorder stops
-- and says so, instead of queueing without bound.
CREATE TABLE memory_backlog (
    event_id uuid PRIMARY KEY REFERENCES memory_events(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    enqueued_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_backlog_owner ON memory_backlog (owner_user_id);
CREATE INDEX memory_backlog_due ON memory_backlog (next_attempt_at);

ALTER TABLE memory_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_events FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_events_owner ON memory_events
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
ALTER TABLE memory_backlog ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_backlog FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_backlog_owner ON memory_backlog
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON memory_events, memory_backlog TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- Events with text become private retrieval documents, which the existing
-- embedding worker embeds and bills to their owner. A revisit that points
-- at another visit is not indexed again.
-- +goose StatementBegin
CREATE FUNCTION misty_memory_index_event() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
DECLARE
    doc_id text;
    indexed text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM ai_retrieval_documents WHERE source_kind = 'memory_' || OLD.kind AND source_id = OLD.id::text;
        RETURN OLD;
    END IF;
    doc_id := 'memory_' || NEW.kind || ':' || NEW.id;
    IF NEW.same_as IS NOT NULL OR btrim(NEW.title || NEW.body) = '' THEN
        DELETE FROM ai_retrieval_documents WHERE id = doc_id;
        RETURN NEW;
    END IF;
    indexed := left(NEW.title || E'\n' || NEW.body, 16000);
    INSERT INTO ai_retrieval_documents(id,source_kind,source_id,owner_user_id,privacy_class,lifecycle_state,source_revision,title,href,metadata)
    VALUES(doc_id,'memory_' || NEW.kind,NEW.id::text,NEW.owner_user_id,'private','active',md5(indexed),NEW.title,NEW.url,
           jsonb_build_object('site',NEW.site,'occurred_at',NEW.occurred_at))
    ON CONFLICT(source_kind,source_id) DO UPDATE SET
      source_revision=EXCLUDED.source_revision,title=EXCLUDED.title,href=EXCLUDED.href,metadata=EXCLUDED.metadata,
      lifecycle_state='active',updated_at=now();
    INSERT INTO ai_retrieval_chunks(document_id,ordinal,content,content_hash)
    VALUES(doc_id,0,indexed,md5(indexed))
    ON CONFLICT(document_id,ordinal) DO UPDATE SET
      content=EXCLUDED.content,content_hash=EXCLUDED.content_hash,
      embedding=CASE WHEN ai_retrieval_chunks.content_hash=EXCLUDED.content_hash THEN ai_retrieval_chunks.embedding ELSE NULL END,
      updated_at=now();
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_events_index AFTER INSERT OR DELETE OR UPDATE OF title, body, same_as ON memory_events
    FOR EACH ROW EXECUTE FUNCTION misty_memory_index_event();

-- The backlog drains as embeddings land.
-- +goose StatementBegin
-- It runs in the embedding worker's service context.
CREATE FUNCTION misty_memory_embedded() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    AS $$
DECLARE embedded_id uuid := split_part(NEW.document_id, ':', 2)::uuid;
BEGIN
    DELETE FROM memory_backlog WHERE memory_backlog.event_id = embedded_id;
    UPDATE memory_events SET indexed_at = now() WHERE id = embedded_id;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_events_embedded AFTER UPDATE OF embedding ON ai_retrieval_chunks
    FOR EACH ROW WHEN (NEW.embedding IS NOT NULL AND NEW.document_id LIKE 'memory\_%')
    EXECUTE FUNCTION misty_memory_embedded();

-- +goose Down
DROP TRIGGER IF EXISTS memory_events_embedded ON ai_retrieval_chunks;
DROP FUNCTION IF EXISTS misty_memory_embedded();
DELETE FROM ai_retrieval_documents WHERE source_kind LIKE 'memory\_%';
DROP TABLE IF EXISTS memory_backlog;
DROP TABLE IF EXISTS memory_events;
DROP FUNCTION IF EXISTS misty_memory_index_event();
