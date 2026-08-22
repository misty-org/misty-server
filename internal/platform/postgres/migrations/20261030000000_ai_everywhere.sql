-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE UNIQUE INDEX IF NOT EXISTS space_runs_ai_handoff_idempotency_idx
    ON space_runs(requesting_member_id,(input->>'ai_idempotency_key'))
    WHERE source_type='agent_console' AND input ? 'ai_idempotency_key';

CREATE TABLE ai_invocations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id TEXT,
    surface_id TEXT NOT NULL CHECK(char_length(surface_id) BETWEEN 1 AND 80),
    mode TEXT NOT NULL CHECK(mode IN ('quick','drawer')),
    trigger_kind TEXT NOT NULL CHECK(trigger_kind IN ('message','selection','object','schedule','event','handoff')),
    state TEXT NOT NULL CHECK(state IN ('queued','running','awaiting_approval','completed','failed','canceled')),
    idempotency_key TEXT NOT NULL CHECK(char_length(idempotency_key) BETWEEN 1 AND 200),
    request_payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(request_payload)='object'),
    error_code TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    canceled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id,idempotency_key)
);
CREATE INDEX ai_invocations_user_updated_idx ON ai_invocations(user_id,updated_at DESC);
CREATE INDEX ai_invocations_expiry_idx ON ai_invocations(expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE ai_invocation_events (
    invocation_id TEXT NOT NULL REFERENCES ai_invocations(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK(sequence>0),
    event_type TEXT NOT NULL CHECK(char_length(event_type) BETWEEN 1 AND 80),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(payload)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(invocation_id,sequence)
);

CREATE TABLE ai_artifacts (
    id TEXT PRIMARY KEY,
    invocation_id TEXT NOT NULL REFERENCES ai_invocations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    schema_version INTEGER NOT NULL CHECK(schema_version>0),
    kind TEXT NOT NULL CHECK(kind IN ('text_patch','task_set','calendar_event','roadmap_patch','drawing_patch','file_plan','mail_draft','message_draft','code_patch','terminal_command','browser_action','transfer_plan','extension_action','image_edit')),
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    sources JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(sources)='array'),
    target JSONB CHECK(target IS NULL OR jsonb_typeof(target)='object'),
    base_revision JSONB,
    operations JSONB NOT NULL DEFAULT '{}'::jsonb,
    risk TEXT NOT NULL CHECK(risk IN ('observe','draft','consequential','dangerous')),
    approval_policy TEXT NOT NULL CHECK(approval_policy IN ('none','visible_apply','confirm','always_confirm')),
    idempotency_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('proposed','applying','applied','rejected','stale','failed')),
    error_message TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id,idempotency_key)
);
CREATE INDEX ai_artifacts_user_created_idx ON ai_artifacts(user_id,created_at DESC);

CREATE TABLE ai_surface_preferences (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface_id TEXT NOT NULL,
    pinned_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL,
    proactive_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    saved_actions JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(saved_actions)='array'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,surface_id)
);

CREATE TABLE ai_user_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    retention_days SMALLINT NOT NULL DEFAULT 30 CHECK(retention_days BETWEEN 1 AND 365),
    purge_state TEXT NOT NULL DEFAULT 'none' CHECK(purge_state IN ('none','queued','working','verified','failed')),
    disabled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE ai_feature_flags (
    surface_id TEXT NOT NULL DEFAULT '*',
    action_id TEXT NOT NULL DEFAULT '*',
    model_id TEXT NOT NULL DEFAULT '*',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    rollout_percent SMALLINT NOT NULL DEFAULT 100 CHECK(rollout_percent BETWEEN 0 AND 100),
    updated_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(surface_id,action_id,model_id)
);
INSERT INTO ai_feature_flags(surface_id,action_id,model_id,enabled,rollout_percent) VALUES('*','*','*',TRUE,100);

CREATE TABLE ai_feedback (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invocation_id TEXT NOT NULL REFERENCES ai_invocations(id) ON DELETE CASCADE,
    rating SMALLINT NOT NULL CHECK(rating IN (-1,1)),
    reason_code TEXT NOT NULL DEFAULT '',
    comment TEXT NOT NULL DEFAULT '' CHECK(char_length(comment)<=2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id,invocation_id)
);

-- Permission-constrained hybrid retrieval. A row belongs either to a private
-- user or to a Space audience; candidates remain protected by RLS before any
-- lexical or vector ranking can occur.
CREATE TABLE ai_retrieval_documents (
    id TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL,
    source_id TEXT NOT NULL,
    owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
    audience_kind TEXT CHECK(audience_kind IN ('space','conversation')),
    audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    privacy_class TEXT NOT NULL CHECK(privacy_class IN ('private','shared','provider')),
    lifecycle_state TEXT NOT NULL DEFAULT 'active' CHECK(lifecycle_state IN ('active','deleted','inaccessible')),
    source_revision TEXT NOT NULL,
    title TEXT NOT NULL,
    href TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metadata)='object'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_kind,source_id),
    CHECK(
      (privacy_class='private' AND owner_user_id IS NOT NULL AND space_id IS NULL AND audience_kind IS NULL) OR
      (privacy_class IN ('shared','provider') AND owner_user_id IS NULL AND space_id IS NOT NULL AND audience_kind IS NOT NULL)
    )
);
CREATE TABLE ai_retrieval_chunks (
    document_id TEXT NOT NULL REFERENCES ai_retrieval_documents(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK(ordinal>=0),
    content TEXT NOT NULL CHECK(char_length(content)<=16000),
    content_hash TEXT NOT NULL,
    lexical TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple',content)) STORED,
    embedding vector(768),
    embedding_model TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(document_id,ordinal)
);
CREATE INDEX ai_retrieval_chunks_lexical_idx ON ai_retrieval_chunks USING GIN(lexical);
CREATE INDEX ai_retrieval_chunks_embedding_idx ON ai_retrieval_chunks USING hnsw(embedding vector_cosine_ops) WHERE embedding IS NOT NULL;
CREATE INDEX ai_retrieval_documents_space_idx ON ai_retrieval_documents(space_id,source_kind,source_id) WHERE lifecycle_state='active';

CREATE TABLE ai_cleanup_jobs (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','working','verified','failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 20),
    error_code TEXT NOT NULL DEFAULT '',
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ai_cleanup_jobs_due_idx ON ai_cleanup_jobs(available_at,created_at) WHERE state IN ('queued','failed');

CREATE OR REPLACE FUNCTION misty_ai_write_index_document(
    candidate_kind TEXT,candidate_id TEXT,candidate_space_id TEXT,candidate_audience_kind TEXT,
    candidate_conversation_id TEXT,candidate_privacy TEXT,candidate_revision TEXT,
    candidate_title TEXT,candidate_href TEXT,candidate_content TEXT,candidate_metadata JSONB
) RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$
DECLARE ai_document_id TEXT := candidate_kind || ':' || candidate_id;
BEGIN
    INSERT INTO ai_retrieval_documents(id,source_kind,source_id,space_id,audience_kind,audience_conversation_id,privacy_class,lifecycle_state,source_revision,title,href,metadata)
    VALUES(ai_document_id,candidate_kind,candidate_id,candidate_space_id,candidate_audience_kind,candidate_conversation_id,candidate_privacy,'active',candidate_revision,candidate_title,candidate_href,COALESCE(candidate_metadata,'{}'::jsonb))
    ON CONFLICT(source_kind,source_id) DO UPDATE SET
      space_id=EXCLUDED.space_id,audience_kind=EXCLUDED.audience_kind,audience_conversation_id=EXCLUDED.audience_conversation_id,
      privacy_class=EXCLUDED.privacy_class,lifecycle_state='active',source_revision=EXCLUDED.source_revision,
      title=EXCLUDED.title,href=EXCLUDED.href,metadata=EXCLUDED.metadata,updated_at=NOW();
    INSERT INTO ai_retrieval_chunks(document_id,ordinal,content,content_hash)
    VALUES(ai_document_id,0,left(candidate_content,16000),md5(candidate_content))
    ON CONFLICT(document_id,ordinal) DO UPDATE SET
      content=EXCLUDED.content,content_hash=EXCLUDED.content_hash,
      embedding=CASE WHEN ai_retrieval_chunks.content_hash=EXCLUDED.content_hash THEN ai_retrieval_chunks.embedding ELSE NULL END,
      embedding_model=CASE WHEN ai_retrieval_chunks.content_hash=EXCLUDED.content_hash THEN ai_retrieval_chunks.embedding_model ELSE '' END,
      updated_at=NOW();
END $$;

CREATE OR REPLACE FUNCTION misty_ai_index_note() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='note' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.lifecycle_state<>'active' THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='note' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('note',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.collaboration_revision::text,NEW.title_projection,'/spaces/'||NEW.space_id||'/notes?note='||NEW.id,NEW.title_projection||E'\n'||NEW.plain_text_projection,'{}');
  RETURN NEW;
END $$;
CREATE TRIGGER ai_index_space_notes AFTER INSERT OR UPDATE OR DELETE ON space_notes FOR EACH ROW EXECUTE FUNCTION misty_ai_index_note();

CREATE OR REPLACE FUNCTION misty_ai_index_task() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='task' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.archived_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='task' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('task',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.version::text,NEW.title,'/spaces/'||NEW.space_id||'/planner/tasks/board?task='||NEW.id,NEW.title||E'\nStatus: '||NEW.status||E'\nPriority: '||NEW.priority||E'\n'||NEW.notes,jsonb_build_object('status',NEW.status,'priority',NEW.priority));
  RETURN NEW;
END $$;
CREATE TRIGGER ai_index_space_tasks AFTER INSERT OR UPDATE OR DELETE ON space_tasks FOR EACH ROW EXECUTE FUNCTION misty_ai_index_task();

CREATE OR REPLACE FUNCTION misty_ai_index_roadmap() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='roadmap' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.archived_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='roadmap' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('roadmap',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.graph_version::text,NEW.name,'/spaces/'||NEW.space_id||'/planner/roadmaps/'||NEW.id,NEW.name||E'\n'||NEW.description,'{}');
  RETURN NEW;
END $$;
CREATE TRIGGER ai_index_space_roadmaps AFTER INSERT OR UPDATE OR DELETE ON space_roadmaps FOR EACH ROW EXECUTE FUNCTION misty_ai_index_roadmap();

CREATE OR REPLACE FUNCTION misty_ai_index_native_calendar() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.archived_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('calendar',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.version::text,NEW.title,'/spaces/'||NEW.space_id||'/planner/agenda/day?date='||to_char(NEW.starts_at,'YYYY-MM-DD'),NEW.title||E'\n'||NEW.description||E'\nLocation: '||NEW.location,jsonb_build_object('starts_at',NEW.starts_at,'ends_at',NEW.ends_at));
  RETURN NEW;
END $$;
CREATE TRIGGER ai_index_space_native_calendar AFTER INSERT OR UPDATE OR DELETE ON space_native_calendar_events FOR EACH ROW EXECUTE FUNCTION misty_ai_index_native_calendar();

CREATE OR REPLACE FUNCTION misty_ai_index_provider_calendar() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.removed_at IS NOT NULL THEN DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=NEW.id; RETURN NEW; END IF;
  PERFORM misty_ai_write_index_document('calendar',NEW.id,NEW.space_id,'space',NULL,'provider',NEW.fingerprint,NEW.title,'/spaces/'||NEW.space_id||'/planner/agenda/day?date='||to_char(NEW.starts_at,'YYYY-MM-DD'),NEW.title||E'\n'||NEW.description||E'\nLocation: '||NEW.location,jsonb_build_object('provider',NEW.provider,'starts_at',NEW.starts_at,'ends_at',NEW.ends_at));
  RETURN NEW;
END $$;
CREATE TRIGGER ai_index_space_provider_calendar AFTER INSERT OR UPDATE OR DELETE ON space_calendar_events FOR EACH ROW EXECUTE FUNCTION misty_ai_index_provider_calendar();

CREATE OR REPLACE FUNCTION misty_ai_index_provider_record() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='provider' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.deleted_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='provider' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('provider',NEW.id,NEW.space_id,'space',NULL,'provider',NEW.fingerprint,COALESCE(NULLIF(NEW.display_name,''),NEW.record_type),'/spaces/'||NEW.space_id||'/connections',COALESCE(NULLIF(NEW.display_name,''),NEW.record_type)||E'\n'||NEW.content::text,jsonb_build_object('provider',NEW.provider,'record_type',NEW.record_type));
  RETURN NEW;
END $$;
CREATE TRIGGER ai_index_provider_content AFTER INSERT OR UPDATE OR DELETE ON provider_content_records FOR EACH ROW EXECUTE FUNCTION misty_ai_index_provider_record();

-- Existing authorized content is available immediately; subsequent domain
-- writes are maintained synchronously by the triggers above.
INSERT INTO ai_retrieval_documents(id,source_kind,source_id,space_id,audience_kind,audience_conversation_id,privacy_class,lifecycle_state,source_revision,title,href)
SELECT 'note:'||id,'note',id,space_id,audience_kind,audience_conversation_id,'shared','active',collaboration_revision::text,title_projection,'/spaces/'||space_id||'/notes?note='||id FROM space_notes WHERE lifecycle_state='active'
ON CONFLICT(source_kind,source_id) DO NOTHING;
INSERT INTO ai_retrieval_chunks(document_id,ordinal,content,content_hash)
SELECT 'note:'||id,0,left(title_projection||E'\n'||plain_text_projection,16000),md5(title_projection||E'\n'||plain_text_projection) FROM space_notes WHERE lifecycle_state='active'
ON CONFLICT(document_id,ordinal) DO NOTHING;
INSERT INTO ai_retrieval_documents(id,source_kind,source_id,space_id,audience_kind,audience_conversation_id,privacy_class,lifecycle_state,source_revision,title,href,metadata)
SELECT 'task:'||id,'task',id,space_id,audience_kind,audience_conversation_id,'shared','active',version::text,title,'/spaces/'||space_id||'/planner/tasks/board?task='||id,jsonb_build_object('status',status,'priority',priority) FROM space_tasks WHERE archived_at IS NULL
ON CONFLICT(source_kind,source_id) DO NOTHING;
INSERT INTO ai_retrieval_chunks(document_id,ordinal,content,content_hash)
SELECT 'task:'||id,0,left(title||E'\nStatus: '||status||E'\nPriority: '||priority||E'\n'||notes,16000),md5(title||status||priority||notes) FROM space_tasks WHERE archived_at IS NULL
ON CONFLICT(document_id,ordinal) DO NOTHING;
SELECT misty_ai_write_index_document('roadmap',id,space_id,audience_kind,audience_conversation_id,'shared',graph_version::text,name,'/spaces/'||space_id||'/planner/roadmaps/'||id,name||E'\n'||description,'{}') FROM space_roadmaps WHERE archived_at IS NULL;
SELECT misty_ai_write_index_document('calendar',id,space_id,audience_kind,audience_conversation_id,'shared',version::text,title,'/spaces/'||space_id||'/planner/agenda/day?date='||to_char(starts_at,'YYYY-MM-DD'),title||E'\n'||description||E'\nLocation: '||location,jsonb_build_object('starts_at',starts_at,'ends_at',ends_at)) FROM space_native_calendar_events WHERE archived_at IS NULL;
SELECT misty_ai_write_index_document('calendar',id,space_id,'space',NULL,'provider',fingerprint,title,'/spaces/'||space_id||'/planner/agenda/day?date='||to_char(starts_at,'YYYY-MM-DD'),title||E'\n'||description||E'\nLocation: '||location,jsonb_build_object('provider',provider,'starts_at',starts_at,'ends_at',ends_at)) FROM space_calendar_events WHERE removed_at IS NULL;
SELECT misty_ai_write_index_document('provider',id,space_id,'space',NULL,'provider',fingerprint,COALESCE(NULLIF(display_name,''),record_type),'/spaces/'||space_id||'/connections',COALESCE(NULLIF(display_name,''),record_type)||E'\n'||content::text,jsonb_build_object('provider',provider,'record_type',record_type)) FROM provider_content_records WHERE deleted_at IS NULL;

ALTER TABLE ai_invocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_invocations FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_invocations_owner_policy ON ai_invocations FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE ai_invocation_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_invocation_events FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_invocation_events_owner_policy ON ai_invocation_events FOR ALL
    USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM ai_invocations i WHERE i.id=invocation_id AND i.user_id=misty_rls_user_id()))
    WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM ai_invocations i WHERE i.id=invocation_id AND i.user_id=misty_rls_user_id()));
ALTER TABLE ai_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_artifacts FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_artifacts_owner_policy ON ai_artifacts FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE ai_surface_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_surface_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_surface_preferences_owner_policy ON ai_surface_preferences FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE ai_user_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_user_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_user_settings_owner_policy ON ai_user_settings FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE ai_feature_flags ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_feature_flags FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_feature_flags_read_policy ON ai_feature_flags FOR SELECT USING(TRUE);
CREATE POLICY ai_feature_flags_service_write_policy ON ai_feature_flags FOR ALL USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
ALTER TABLE ai_feedback ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_feedback FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_feedback_owner_policy ON ai_feedback FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE ai_retrieval_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_retrieval_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_retrieval_documents_read_policy ON ai_retrieval_documents FOR SELECT USING(
    misty_rls_is_service() OR lifecycle_state='active' AND (
      (privacy_class='private' AND owner_user_id=misty_rls_user_id()) OR
      (privacy_class='shared' AND misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id)) OR
      (privacy_class='provider' AND misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id) AND (
        (source_kind='provider' AND EXISTS(
          SELECT 1 FROM provider_content_records p JOIN provider_shared_resources r ON r.id=p.shared_resource_id
          WHERE p.id=source_id AND p.deleted_at IS NULL AND r.status='active'
        )) OR
        (source_kind='calendar' AND EXISTS(
          SELECT 1 FROM space_calendar_events e JOIN space_calendar_sources s ON s.id=e.source_id
          WHERE e.id=source_id AND e.removed_at IS NULL AND s.status='active'
        ))
      ))
    )
);
CREATE POLICY ai_retrieval_documents_service_write_policy ON ai_retrieval_documents FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
CREATE POLICY ai_retrieval_documents_private_delete_policy ON ai_retrieval_documents FOR DELETE
    USING(owner_user_id=misty_rls_user_id() AND privacy_class='private');
ALTER TABLE ai_retrieval_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_retrieval_chunks FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_retrieval_chunks_read_policy ON ai_retrieval_chunks FOR SELECT USING(
    misty_rls_is_service() OR EXISTS(SELECT 1 FROM ai_retrieval_documents d WHERE d.id=document_id)
);
CREATE POLICY ai_retrieval_chunks_service_write_policy ON ai_retrieval_chunks FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
ALTER TABLE ai_cleanup_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_cleanup_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_cleanup_jobs_owner_policy ON ai_cleanup_jobs FOR SELECT
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY ai_cleanup_jobs_owner_insert_policy ON ai_cleanup_jobs FOR INSERT
    WITH CHECK(user_id=misty_rls_user_id());
CREATE POLICY ai_cleanup_jobs_service_write_policy ON ai_cleanup_jobs FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON ai_invocations,ai_invocation_events,ai_artifacts,ai_surface_preferences,ai_user_settings,ai_feature_flags,ai_feedback,ai_retrieval_documents,ai_retrieval_chunks,ai_cleanup_jobs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS space_runs_ai_handoff_idempotency_idx;
-- +goose StatementBegin
DROP TABLE IF EXISTS ai_cleanup_jobs;
DROP TRIGGER IF EXISTS ai_index_provider_content ON provider_content_records;
DROP TRIGGER IF EXISTS ai_index_space_provider_calendar ON space_calendar_events;
DROP TRIGGER IF EXISTS ai_index_space_native_calendar ON space_native_calendar_events;
DROP TRIGGER IF EXISTS ai_index_space_roadmaps ON space_roadmaps;
DROP TRIGGER IF EXISTS ai_index_space_tasks ON space_tasks;
DROP TRIGGER IF EXISTS ai_index_space_notes ON space_notes;
DROP FUNCTION IF EXISTS misty_ai_index_provider_record();
DROP FUNCTION IF EXISTS misty_ai_index_provider_calendar();
DROP FUNCTION IF EXISTS misty_ai_index_native_calendar();
DROP FUNCTION IF EXISTS misty_ai_index_roadmap();
DROP FUNCTION IF EXISTS misty_ai_index_task();
DROP FUNCTION IF EXISTS misty_ai_index_note();
DROP FUNCTION IF EXISTS misty_ai_write_index_document(TEXT,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT,JSONB);
DROP TABLE IF EXISTS ai_retrieval_chunks;
DROP TABLE IF EXISTS ai_retrieval_documents;
DROP TABLE IF EXISTS ai_feedback;
DROP TABLE IF EXISTS ai_feature_flags;
DROP TABLE IF EXISTS ai_user_settings;
DROP TABLE IF EXISTS ai_surface_preferences;
DROP TABLE IF EXISTS ai_artifacts;
DROP TABLE IF EXISTS ai_invocation_events;
DROP TABLE IF EXISTS ai_invocations;
-- +goose StatementEnd
