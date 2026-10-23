-- +goose Up
-- The memory tree per person per Space (docs/plans/spaces-as-profiles.md,
-- phase 7). Moments (memory_events, memory_backlog), Summaries (ai_recaps),
-- Facts (misty_memories) and Workflows (agent_methods, agent_method_versions,
-- agent_method_lessons) each belong to one person in one Space. The server
-- keeps them readable and private to their owner, and searches them by
-- meaning through memory_chunks. There are no users, so existing rows join
-- their owner's personal Space and rows of accounts without one are dropped.

CREATE TEMP TABLE memory_personal_spaces AS
    SELECT id AS user_id, misty_ensure_personal_space(id) AS space_id FROM users;

-- misty_memories carried a nullable space_id that was never written.
ALTER TABLE misty_memories DROP CONSTRAINT misty_memories_content_space_fkey;
DROP INDEX misty_memories_active_space_idx;
DROP INDEX misty_memories_agent_idx;
ALTER TABLE memory_events ADD COLUMN space_id text;
ALTER TABLE memory_backlog ADD COLUMN space_id text;
ALTER TABLE ai_recaps ADD COLUMN space_id text;
ALTER TABLE agent_methods ADD COLUMN space_id text;
ALTER TABLE agent_method_versions ADD COLUMN space_id text;
ALTER TABLE agent_method_lessons ADD COLUMN space_id text;

UPDATE memory_events t SET space_id=p.space_id FROM memory_personal_spaces p WHERE p.user_id=t.owner_user_id;
UPDATE memory_backlog t SET space_id=e.space_id FROM memory_events e WHERE e.id=t.event_id;
UPDATE ai_recaps t SET space_id=p.space_id FROM memory_personal_spaces p WHERE p.user_id=t.user_id;
UPDATE misty_memories t SET space_id=p.space_id FROM memory_personal_spaces p WHERE p.user_id=t.user_id;
UPDATE agent_methods t SET space_id=p.space_id FROM memory_personal_spaces p WHERE p.user_id=t.user_id;
UPDATE agent_method_versions t SET space_id=m.space_id FROM agent_methods m WHERE m.id=t.method_id AND m.user_id=t.user_id;
UPDATE agent_method_lessons t SET space_id=m.space_id FROM agent_methods m WHERE m.id=t.method_id AND m.user_id=t.user_id;
DELETE FROM memory_backlog WHERE space_id IS NULL;
DELETE FROM memory_events WHERE space_id IS NULL;
DELETE FROM ai_recaps WHERE space_id IS NULL;
DELETE FROM misty_memories WHERE space_id IS NULL;
DELETE FROM agent_method_lessons WHERE space_id IS NULL;
DELETE FROM agent_method_versions WHERE space_id IS NULL;
DELETE FROM agent_methods WHERE space_id IS NULL;
DROP TABLE memory_personal_spaces;

-- Leaving a Space removes its member rows through the leave job (phase 9);
-- a Space that is deleted takes its memory with it.
ALTER TABLE memory_events ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT memory_events_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE memory_backlog ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT memory_backlog_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE ai_recaps ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT ai_recaps_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE misty_memories ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT misty_memories_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE agent_methods ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT agent_methods_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE agent_method_versions ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT agent_method_versions_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE agent_method_lessons ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT agent_method_lessons_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;

-- The same words remembered in two Spaces are two memories, and each Space
-- has its own recaps.
ALTER TABLE misty_memories DROP CONSTRAINT misty_memories_user_id_scope_key_memory_key_key,
    ADD CONSTRAINT misty_memories_space_memory_key UNIQUE (user_id, space_id, scope_key, memory_key);
ALTER TABLE ai_recaps DROP CONSTRAINT ai_recaps_pkey, ADD PRIMARY KEY (user_id, space_id, surface_id);

DROP INDEX memory_events_owner_time;
DROP INDEX memory_events_owner_url;
DROP INDEX memory_events_owner_site;
-- Search is by meaning only.
DROP INDEX memory_events_body_search;
CREATE INDEX memory_events_owner_time ON memory_events (owner_user_id, space_id, occurred_at DESC);
CREATE INDEX memory_events_owner_url ON memory_events (owner_user_id, space_id, url, occurred_at DESC) WHERE url <> '';
CREATE INDEX memory_events_owner_site ON memory_events (owner_user_id, space_id, site);
CREATE INDEX memory_backlog_owner_space ON memory_backlog (owner_user_id, space_id);
CREATE INDEX misty_memories_active_space_idx ON misty_memories (user_id, space_id, updated_at DESC) WHERE forgotten_at IS NULL;
CREATE INDEX misty_memories_agent_idx ON misty_memories (user_id, space_id, agent_id) WHERE forgotten_at IS NULL;
DROP INDEX agent_methods_owner;
CREATE INDEX agent_methods_owner ON agent_methods (user_id, space_id, agent_id, updated_at DESC);

-- +goose StatementBegin
-- A row written without a Space lands in its owner's personal Space, and a
-- row can only live in a Space its owner belongs to. The owner column is the
-- trigger's argument. A row's Space never changes.
CREATE FUNCTION misty_memory_space_guard() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE owner text := to_jsonb(NEW)->>TG_ARGV[0];
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.space_id IS DISTINCT FROM OLD.space_id THEN
      RAISE EXCEPTION 'memory cannot move between Spaces' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.space_id IS NULL THEN
    NEW.space_id := misty_ensure_personal_space(owner);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active'
       WHERE m.space_id=NEW.space_id AND m.user_id=owner) THEN
    RAISE EXCEPTION 'memory can only live in a Space its owner belongs to' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_events_space BEFORE INSERT OR UPDATE OF space_id ON memory_events
    FOR EACH ROW EXECUTE FUNCTION misty_memory_space_guard('owner_user_id');
CREATE TRIGGER ai_recaps_space BEFORE INSERT OR UPDATE OF space_id ON ai_recaps
    FOR EACH ROW EXECUTE FUNCTION misty_memory_space_guard('user_id');
CREATE TRIGGER misty_memories_space BEFORE INSERT OR UPDATE OF space_id ON misty_memories
    FOR EACH ROW EXECUTE FUNCTION misty_memory_space_guard('user_id');
CREATE TRIGGER agent_methods_space BEFORE INSERT OR UPDATE OF space_id ON agent_methods
    FOR EACH ROW EXECUTE FUNCTION misty_memory_space_guard('user_id');

-- +goose StatementBegin
-- Rows under a parent take the parent's Space.
CREATE FUNCTION misty_memory_child_space() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  IF TG_TABLE_NAME = 'memory_backlog' THEN
    SELECT space_id INTO NEW.space_id FROM memory_events WHERE id=NEW.event_id;
  ELSE
    SELECT space_id INTO NEW.space_id FROM agent_methods WHERE id=NEW.method_id AND user_id=NEW.user_id;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_backlog_space BEFORE INSERT OR UPDATE OF space_id ON memory_backlog
    FOR EACH ROW EXECUTE FUNCTION misty_memory_child_space();
CREATE TRIGGER agent_method_versions_space BEFORE INSERT OR UPDATE OF space_id ON agent_method_versions
    FOR EACH ROW EXECUTE FUNCTION misty_memory_child_space();
CREATE TRIGGER agent_method_lessons_space BEFORE INSERT OR UPDATE OF space_id ON agent_method_lessons
    FOR EACH ROW EXECUTE FUNCTION misty_memory_child_space();

-- Owner and Space: a row opens only for its owner, and only while they are a
-- member of its Space. Phase 9 adds the agent check on top, in the tools.
DROP POLICY memory_events_owner ON memory_events;
CREATE POLICY memory_events_owner ON memory_events
    USING (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
DROP POLICY memory_backlog_owner ON memory_backlog;
CREATE POLICY memory_backlog_owner ON memory_backlog
    USING (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
DROP POLICY ai_recaps_owner_policy ON ai_recaps;
CREATE POLICY ai_recaps_owner_policy ON ai_recaps
    USING (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
DROP POLICY misty_memories_owner_policy ON misty_memories;
CREATE POLICY misty_memories_owner_policy ON misty_memories
    USING (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
DROP POLICY agent_methods_owner ON agent_methods;
CREATE POLICY agent_methods_owner ON agent_methods
    USING (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
DROP POLICY agent_method_versions_owner ON agent_method_versions;
CREATE POLICY agent_method_versions_owner ON agent_method_versions
    USING (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
DROP POLICY agent_method_lessons_owner ON agent_method_lessons;
CREATE POLICY agent_method_lessons_owner ON agent_method_lessons
    USING (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));

-- One search index for the whole tree. Each record's text is kept with its
-- vector; the embedding worker fills vectors on Misty's keys and bills the
-- owner as background work. A chunk waits until due_at, so moments are
-- embedded in hourly batches.
CREATE TABLE memory_chunks (
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    branch text NOT NULL CHECK (branch IN ('moments', 'summaries', 'facts', 'workflows')),
    record_id text NOT NULL CHECK (char_length(record_id) BETWEEN 1 AND 300),
    chunk integer NOT NULL DEFAULT 0 CHECK (chunk >= 0),
    text text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 16000),
    content_hash text NOT NULL CHECK (char_length(content_hash) = 32),
    embedding vector(768),
    embedding_model text NOT NULL DEFAULT '' CHECK (char_length(embedding_model) <= 200),
    embedding_attempt_id uuid,
    embedding_lease_until timestamptz,
    embedding_attempts integer NOT NULL DEFAULT 0 CHECK (embedding_attempts >= 0),
    due_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (branch, record_id, chunk)
);
CREATE INDEX memory_chunks_embedding_idx ON memory_chunks USING hnsw (embedding vector_cosine_ops) WHERE embedding IS NOT NULL;
CREATE INDEX memory_chunks_owner_space ON memory_chunks (owner_user_id, space_id, branch);
CREATE INDEX memory_chunks_pending ON memory_chunks (due_at) WHERE embedding IS NULL;
CREATE INDEX memory_chunks_same_text ON memory_chunks (owner_user_id, space_id, content_hash) WHERE embedding IS NOT NULL;
ALTER TABLE memory_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_chunks FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_chunks_owner ON memory_chunks
    USING (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON memory_chunks TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_chunks_worker_notify AFTER INSERT OR DELETE OR UPDATE OF embedding, embedding_lease_until ON memory_chunks
    FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('embedding');

-- +goose StatementBegin
-- misty_memory_chunk_put keeps one record's text in the index. Unchanged text
-- keeps its vector; text the same person already has in that Space reuses its
-- vector instead of paying for another. Empty text removes the record.
CREATE FUNCTION misty_memory_chunk_put(p_space text, p_owner text, p_branch text, p_record text, p_text text, p_due timestamptz)
    RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE
    body text := left(btrim(p_text), 16000);
    hash text;
    reuse_vector vector(768);
    reuse_model text;
BEGIN
    IF body = '' THEN
        DELETE FROM memory_chunks WHERE branch=p_branch AND record_id=p_record;
        RETURN;
    END IF;
    hash := md5(body);
    IF EXISTS(SELECT 1 FROM memory_chunks WHERE branch=p_branch AND record_id=p_record AND chunk=0 AND content_hash=hash) THEN
        RETURN;
    END IF;
    SELECT embedding, embedding_model INTO reuse_vector, reuse_model FROM memory_chunks
        WHERE owner_user_id=p_owner AND space_id=p_space AND content_hash=hash AND embedding IS NOT NULL LIMIT 1;
    INSERT INTO memory_chunks(space_id,owner_user_id,branch,record_id,chunk,text,content_hash,embedding,embedding_model,due_at)
    VALUES(p_space,p_owner,p_branch,p_record,0,body,hash,reuse_vector,COALESCE(reuse_model,''),p_due)
    ON CONFLICT(branch,record_id,chunk) DO UPDATE SET text=EXCLUDED.text,content_hash=EXCLUDED.content_hash,
        embedding=EXCLUDED.embedding,embedding_model=EXCLUDED.embedding_model,embedding_attempt_id=NULL,
        embedding_lease_until=NULL,embedding_attempts=0,due_at=EXCLUDED.due_at,updated_at=now();
END $$;
-- +goose StatementEnd

-- Moments leave the shared retrieval index for memory_chunks.
DROP TRIGGER memory_events_index ON memory_events;
DROP TRIGGER memory_events_embedded ON ai_retrieval_chunks;
DROP FUNCTION misty_memory_index_event();
DROP FUNCTION misty_memory_embedded();

-- +goose StatementBegin
-- A moment with text is indexed an hour after it happened, with that hour's
-- other moments. A revisit that points at another visit is not indexed again.
CREATE FUNCTION misty_memory_index_moment() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM memory_chunks WHERE branch='moments' AND record_id=OLD.id::text;
        RETURN OLD;
    END IF;
    PERFORM misty_memory_chunk_put(NEW.space_id, NEW.owner_user_id, 'moments', NEW.id::text,
        CASE WHEN NEW.same_as IS NULL THEN NEW.title || E'\n' || NEW.body ELSE '' END,
        date_trunc('hour', NEW.created_at) + interval '1 hour');
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_events_index AFTER INSERT OR DELETE OR UPDATE OF title, body, same_as ON memory_events
    FOR EACH ROW EXECUTE FUNCTION misty_memory_index_moment();

-- +goose StatementBegin
-- Facts are indexed until they are forgotten.
CREATE FUNCTION misty_memory_index_fact() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM memory_chunks WHERE branch='facts' AND record_id=OLD.id;
        RETURN OLD;
    END IF;
    PERFORM misty_memory_chunk_put(NEW.space_id, NEW.user_id, 'facts', NEW.id,
        CASE WHEN NEW.forgotten_at IS NULL THEN NEW.content ELSE '' END, now());
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER misty_memories_index AFTER INSERT OR DELETE OR UPDATE OF content, forgotten_at ON misty_memories
    FOR EACH ROW EXECUTE FUNCTION misty_memory_index_fact();

-- +goose StatementBegin
-- A summary is indexed by its latest result.
CREATE FUNCTION misty_memory_index_summary() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM memory_chunks WHERE branch='summaries' AND record_id=OLD.space_id || ':' || OLD.user_id || ':' || OLD.surface_id;
        RETURN OLD;
    END IF;
    PERFORM misty_memory_chunk_put(NEW.space_id, NEW.user_id, 'summaries', NEW.space_id || ':' || NEW.user_id || ':' || NEW.surface_id,
        NEW.last_result, now());
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER ai_recaps_index AFTER INSERT OR DELETE OR UPDATE OF last_result ON ai_recaps
    FOR EACH ROW EXECUTE FUNCTION misty_memory_index_summary();

-- +goose StatementBegin
-- A workflow is indexed by its newest request: title, description and
-- instructions. Removing the workflow removes it.
CREATE FUNCTION misty_memory_index_workflow() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
    IF TG_TABLE_NAME = 'agent_methods' THEN
        DELETE FROM memory_chunks WHERE branch='workflows' AND record_id=OLD.id;
        RETURN OLD;
    END IF;
    PERFORM misty_memory_chunk_put(NEW.space_id, NEW.user_id, 'workflows', NEW.method_id,
        concat_ws(E'\n', NEW.definition->>'title', NEW.definition->>'description', NEW.definition->>'instructions'), now());
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER agent_method_versions_index AFTER INSERT ON agent_method_versions
    FOR EACH ROW EXECUTE FUNCTION misty_memory_index_workflow();
CREATE TRIGGER agent_methods_unindex AFTER DELETE ON agent_methods
    FOR EACH ROW EXECUTE FUNCTION misty_memory_index_workflow();

-- +goose StatementBegin
-- A moment's place in the backlog ends when its chunk is embedded.
CREATE FUNCTION misty_memory_moment_embedded() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
    DELETE FROM memory_backlog WHERE event_id = NEW.record_id::uuid;
    UPDATE memory_events SET indexed_at = now() WHERE id = NEW.record_id::uuid;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER memory_chunks_moment_embedded AFTER INSERT OR UPDATE OF embedding ON memory_chunks
    FOR EACH ROW WHEN (NEW.embedding IS NOT NULL AND NEW.branch = 'moments')
    EXECUTE FUNCTION misty_memory_moment_embedded();

-- Backfill: every existing record joins the index. Vectors already made for
-- moments in the retrieval index are kept; the rest are embedded by the
-- background worker.
INSERT INTO memory_chunks(space_id,owner_user_id,branch,record_id,text,content_hash,embedding,embedding_model,due_at)
SELECT e.space_id, e.owner_user_id, 'moments', e.id::text, t.body, md5(t.body), c.embedding,
       COALESCE(CASE WHEN c.embedding IS NULL THEN '' ELSE c.embedding_model END, ''), now()
FROM memory_events e
CROSS JOIN LATERAL (SELECT left(btrim(e.title || E'\n' || e.body), 16000) AS body) t
LEFT JOIN ai_retrieval_chunks c ON c.document_id = 'memory_' || e.kind || ':' || e.id AND c.ordinal = 0 AND c.content_hash = md5(left(e.title || E'\n' || e.body, 16000))
WHERE e.same_as IS NULL AND t.body <> ''
ON CONFLICT DO NOTHING;
INSERT INTO memory_chunks(space_id,owner_user_id,branch,record_id,text,content_hash)
SELECT space_id, user_id, 'facts', id, left(btrim(content), 16000), md5(left(btrim(content), 16000))
FROM misty_memories WHERE forgotten_at IS NULL
ON CONFLICT DO NOTHING;
INSERT INTO memory_chunks(space_id,owner_user_id,branch,record_id,text,content_hash)
SELECT space_id, user_id, 'summaries', space_id || ':' || user_id || ':' || surface_id, left(btrim(last_result), 16000), md5(left(btrim(last_result), 16000))
FROM ai_recaps WHERE btrim(last_result) <> ''
ON CONFLICT DO NOTHING;
INSERT INTO memory_chunks(space_id,owner_user_id,branch,record_id,text,content_hash)
SELECT m.space_id, m.user_id, 'workflows', m.id, t.body, md5(t.body)
FROM agent_methods m
JOIN agent_method_versions v ON v.method_id = m.id AND v.version = m.current_version
CROSS JOIN LATERAL (SELECT left(btrim(concat_ws(E'\n', v.definition->>'title', v.definition->>'description', v.definition->>'instructions')), 16000) AS body) t
WHERE t.body <> ''
ON CONFLICT DO NOTHING;
UPDATE memory_events e SET indexed_at = COALESCE(e.indexed_at, now())
    FROM memory_chunks c WHERE c.branch = 'moments' AND c.record_id = e.id::text AND c.embedding IS NOT NULL;
DELETE FROM memory_backlog b USING memory_chunks c
    WHERE c.branch = 'moments' AND c.record_id = b.event_id::text AND c.embedding IS NOT NULL;
DELETE FROM ai_retrieval_documents WHERE source_kind LIKE 'memory\_%';

-- +goose Down
-- Space memory cannot fold back into one tree without merging people's
-- Spaces; going down drops every row outside the personal Space.
DROP TABLE memory_chunks;
DROP FUNCTION misty_memory_chunk_put(text, text, text, text, text, timestamptz);
DROP TRIGGER memory_events_index ON memory_events;
DROP TRIGGER misty_memories_index ON misty_memories;
DROP TRIGGER ai_recaps_index ON ai_recaps;
DROP TRIGGER agent_method_versions_index ON agent_method_versions;
DROP TRIGGER agent_methods_unindex ON agent_methods;
DROP FUNCTION misty_memory_index_moment();
DROP FUNCTION misty_memory_index_fact();
DROP FUNCTION misty_memory_index_summary();
DROP FUNCTION misty_memory_index_workflow();
DROP FUNCTION misty_memory_moment_embedded();
DROP TRIGGER memory_events_space ON memory_events;
DROP TRIGGER ai_recaps_space ON ai_recaps;
DROP TRIGGER misty_memories_space ON misty_memories;
DROP TRIGGER agent_methods_space ON agent_methods;
DROP TRIGGER memory_backlog_space ON memory_backlog;
DROP TRIGGER agent_method_versions_space ON agent_method_versions;
DROP TRIGGER agent_method_lessons_space ON agent_method_lessons;
DROP FUNCTION misty_memory_space_guard();
DROP FUNCTION misty_memory_child_space();

DELETE FROM memory_events e WHERE NOT EXISTS(SELECT 1 FROM spaces s WHERE s.id=e.space_id AND s.is_default AND s.owner_user_id=e.owner_user_id);
DELETE FROM ai_recaps r WHERE NOT EXISTS(SELECT 1 FROM spaces s WHERE s.id=r.space_id AND s.is_default AND s.owner_user_id=r.user_id);
DELETE FROM misty_memories m WHERE NOT EXISTS(SELECT 1 FROM spaces s WHERE s.id=m.space_id AND s.is_default AND s.owner_user_id=m.user_id);
DELETE FROM agent_methods m WHERE NOT EXISTS(SELECT 1 FROM spaces s WHERE s.id=m.space_id AND s.is_default AND s.owner_user_id=m.user_id);

DROP POLICY memory_events_owner ON memory_events;
CREATE POLICY memory_events_owner ON memory_events
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
DROP POLICY memory_backlog_owner ON memory_backlog;
CREATE POLICY memory_backlog_owner ON memory_backlog
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
DROP POLICY ai_recaps_owner_policy ON ai_recaps;
CREATE POLICY ai_recaps_owner_policy ON ai_recaps
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
DROP POLICY misty_memories_owner_policy ON misty_memories;
CREATE POLICY misty_memories_owner_policy ON misty_memories
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
DROP POLICY agent_methods_owner ON agent_methods;
CREATE POLICY agent_methods_owner ON agent_methods
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
DROP POLICY agent_method_versions_owner ON agent_method_versions;
CREATE POLICY agent_method_versions_owner ON agent_method_versions
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
DROP POLICY agent_method_lessons_owner ON agent_method_lessons;
CREATE POLICY agent_method_lessons_owner ON agent_method_lessons
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

DROP INDEX agent_methods_owner;
CREATE INDEX agent_methods_owner ON agent_methods (user_id, agent_id, updated_at DESC);
DROP INDEX misty_memories_active_space_idx;
DROP INDEX misty_memories_agent_idx;
DROP INDEX memory_backlog_owner_space;
DROP INDEX memory_events_owner_time;
DROP INDEX memory_events_owner_url;
DROP INDEX memory_events_owner_site;
CREATE INDEX memory_events_owner_time ON memory_events (owner_user_id, occurred_at DESC);
CREATE INDEX memory_events_owner_url ON memory_events (owner_user_id, url, occurred_at DESC) WHERE url <> '';
CREATE INDEX memory_events_owner_site ON memory_events (owner_user_id, site);
CREATE INDEX memory_events_body_search ON memory_events USING gin (to_tsvector('simple', title || ' ' || body));
ALTER TABLE ai_recaps DROP CONSTRAINT ai_recaps_pkey, ADD PRIMARY KEY (user_id, surface_id);
ALTER TABLE misty_memories DROP CONSTRAINT misty_memories_space_memory_key,
    ADD CONSTRAINT misty_memories_user_id_scope_key_memory_key_key UNIQUE (user_id, scope_key, memory_key);
ALTER TABLE agent_method_lessons DROP COLUMN space_id;
ALTER TABLE agent_method_versions DROP COLUMN space_id;
ALTER TABLE agent_methods DROP COLUMN space_id;
ALTER TABLE ai_recaps DROP COLUMN space_id;
ALTER TABLE memory_backlog DROP COLUMN space_id;
ALTER TABLE memory_events DROP COLUMN space_id;
ALTER TABLE misty_memories DROP CONSTRAINT misty_memories_space_fkey, ALTER COLUMN space_id DROP NOT NULL;
UPDATE misty_memories SET space_id = NULL;
ALTER TABLE misty_memories ADD CONSTRAINT misty_memories_content_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE SET NULL;
CREATE INDEX misty_memories_active_space_idx ON misty_memories (user_id, space_id, updated_at DESC) WHERE forgotten_at IS NULL AND space_id IS NOT NULL;
CREATE INDEX misty_memories_agent_idx ON misty_memories (user_id, agent_id, space_id) WHERE forgotten_at IS NULL;
-- Moments are no longer indexed after going down: the retrieval-index trigger
-- from 20271021000000 is not recreated.
