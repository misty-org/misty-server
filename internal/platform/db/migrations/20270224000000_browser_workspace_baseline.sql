-- Browser workspace baseline. Generated from an empty, upgraded PostgreSQL 16 database.
-- Fresh installs do not execute historical pricing or retired billing migrations.
-- Existing installations at 20270215120000 or later upgrade in place without
-- rebuilding tables. Earlier installations require the archived operator upgrade.
-- +goose Up
-- +goose StatementBegin
DO $misty_migration$
DECLARE previous_version bigint;
BEGIN
 PERFORM set_config('app.rls_mode','service',true);
 IF to_regclass('public.users') IS NULL THEN
  IF EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename<>'goose_db_version') THEN
   RAISE EXCEPTION 'Refusing baseline over an unrecognized partial database';
  END IF;
  CREATE EXTENSION IF NOT EXISTS vector;
  PERFORM set_config('check_function_bodies','off',true);
  EXECUTE $misty_schema$
CREATE SCHEMA IF NOT EXISTS public;

COMMENT ON SCHEMA public IS 'standard public schema';

CREATE FUNCTION public.clear_roadmap_manual_completion_for_reopened_task() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.archived_at IS NULL AND NEW.status <> 'canceled'
       AND (OLD.archived_at IS DISTINCT FROM NEW.archived_at OR OLD.status IS DISTINCT FROM NEW.status) THEN
        UPDATE space_roadmap_goals g
        SET manual_completed_at=NULL,manual_completed_by_user_id=NULL,version=version+1,updated_at=NOW()
        WHERE g.manual_completed_at IS NOT NULL
          AND EXISTS (
              SELECT 1 FROM space_roadmap_goal_tasks gt
              WHERE gt.goal_id=g.id AND gt.task_id=NEW.id
          );
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_index_native_calendar() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.archived_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('calendar',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.version::text,NEW.title,'/spaces/'||NEW.space_id||'/planner/agenda/day?date='||to_char(NEW.starts_at,'YYYY-MM-DD'),NEW.title||E'\n'||NEW.description||E'\nLocation: '||NEW.location,jsonb_build_object('starts_at',NEW.starts_at,'ends_at',NEW.ends_at));
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_index_note() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='note' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.lifecycle_state<>'active' THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='note' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('note',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.collaboration_revision::text,NEW.title_projection,'/spaces/'||NEW.space_id||'/notes?note='||NEW.id,NEW.title_projection||E'\n'||NEW.plain_text_projection,'{}');
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_index_provider_calendar() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.removed_at IS NOT NULL THEN DELETE FROM ai_retrieval_documents WHERE source_kind='calendar' AND source_id=NEW.id; RETURN NEW; END IF;
  PERFORM misty_ai_write_index_document('calendar',NEW.id,NEW.space_id,'space',NULL,'provider',NEW.fingerprint,NEW.title,'/spaces/'||NEW.space_id||'/planner/agenda/day?date='||to_char(NEW.starts_at,'YYYY-MM-DD'),NEW.title||E'\n'||NEW.description||E'\nLocation: '||NEW.location,jsonb_build_object('provider',NEW.provider,'starts_at',NEW.starts_at,'ends_at',NEW.ends_at));
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_index_provider_record() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='provider' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.deleted_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='provider' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('provider',NEW.id,NEW.space_id,'space',NULL,'provider',NEW.fingerprint,COALESCE(NULLIF(NEW.display_name,''),NEW.record_type),'/spaces/'||NEW.space_id||'/connections',COALESCE(NULLIF(NEW.display_name,''),NEW.record_type)||E'\n'||NEW.content::text,jsonb_build_object('provider',NEW.provider,'record_type',NEW.record_type));
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_index_roadmap() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='roadmap' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.archived_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='roadmap' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('roadmap',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.graph_version::text,NEW.name,'/spaces/'||NEW.space_id||'/planner/roadmaps/'||NEW.id,NEW.name||E'\n'||NEW.description,'{}');
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_index_task() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$ BEGIN
  IF TG_OP='DELETE' THEN DELETE FROM ai_retrieval_documents WHERE source_kind='task' AND source_id=OLD.id; RETURN OLD; END IF;
  IF NEW.archived_at IS NOT NULL THEN
    DELETE FROM ai_retrieval_documents WHERE source_kind='task' AND source_id=NEW.id; RETURN NEW;
  END IF;
  PERFORM misty_ai_write_index_document('task',NEW.id,NEW.space_id,NEW.audience_kind,NEW.audience_conversation_id,'shared',NEW.version::text,NEW.title,'/spaces/'||NEW.space_id||'/planner/tasks/board?task='||NEW.id,NEW.title||E'\nStatus: '||NEW.status||E'\nPriority: '||NEW.priority||E'\n'||NEW.notes,jsonb_build_object('status',NEW.status,'priority',NEW.priority));
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_ai_write_index_document(candidate_kind text, candidate_id text, candidate_space_id text, candidate_audience_kind text, candidate_conversation_id text, candidate_privacy text, candidate_revision text, candidate_title text, candidate_href text, candidate_content text, candidate_metadata jsonb) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
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

CREATE FUNCTION public.misty_can_access_security_domain(candidate_domain_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
    SELECT EXISTS(
        SELECT 1 FROM security_domains d
        WHERE d.id=candidate_domain_id AND (
            d.owner_user_id=misty_rls_user_id() OR
            (d.space_id IS NOT NULL AND EXISTS(
                SELECT 1 FROM space_members m WHERE m.space_id=d.space_id AND m.user_id=misty_rls_user_id()
            ))
        )
    )
$$;

CREATE FUNCTION public.misty_can_access_space_audience(candidate_space_id text, candidate_audience_kind text, candidate_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
    SELECT CASE
        WHEN candidate_audience_kind='space'
            THEN misty_is_space_member(candidate_space_id)
        WHEN candidate_audience_kind='conversation'
            THEN candidate_conversation_id IS NOT NULL
             AND EXISTS(
                SELECT 1
                FROM space_conversation_members cm
                JOIN space_conversations c ON c.id=cm.conversation_id
                JOIN space_members sm ON sm.space_id=c.space_id AND sm.user_id=cm.user_id
                WHERE cm.conversation_id=candidate_conversation_id
                  AND c.space_id=candidate_space_id
                  AND cm.actor_kind='person'
                  AND cm.user_id=misty_rls_user_id()
             )
        ELSE FALSE
    END
$$;

CREATE FUNCTION public.misty_cancel_run_device_work() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.state IN ('completed','completed_with_errors','failed','canceled') AND NEW.state IS DISTINCT FROM OLD.state THEN
   UPDATE workflow_device_node_jobs SET
    cancel_requested_at=COALESCE(cancel_requested_at,clock_timestamp()),
    state=CASE WHEN state='queued' OR state='leased' AND control_version=2 AND execution_started_at IS NULL THEN 'canceled' ELSE state END,
    completed_at=CASE WHEN state='queued' OR state='leased' AND control_version=2 AND execution_started_at IS NULL THEN clock_timestamp() ELSE completed_at END
   WHERE (run_id=NEW.id OR invocation_id=NEW.id) AND state IN ('queued','leased','executing');
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION public.misty_default_agent_run_owner() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.owner_user_id IS NULL OR NEW.owner_user_id='' THEN
        NEW.owner_user_id := NEW.requesting_member_id;
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.misty_is_shared_space_run_visible(candidate_space_id text, candidate_source_type text, candidate_source_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
    SELECT CASE
        WHEN candidate_source_type='schedule' THEN misty_is_space_member(candidate_space_id)
        WHEN candidate_source_type='group_mention' AND EXISTS(
            SELECT 1 FROM space_conversations c
            WHERE c.id=candidate_source_conversation_id AND c.space_id=candidate_space_id
        ) THEN misty_is_space_conversation_member(candidate_source_conversation_id)
        WHEN candidate_source_type='group_mention' THEN misty_is_space_member(candidate_space_id)
        ELSE FALSE
    END
$$;

CREATE FUNCTION public.misty_is_space_conversation_member(candidate_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
    SELECT EXISTS (
        SELECT 1
        FROM space_conversations c
        JOIN space_members sm ON sm.space_id=c.space_id
        WHERE c.id=candidate_conversation_id
          AND sm.user_id=misty_rls_user_id()
          AND (
              c.visible_to_space OR EXISTS (
                  SELECT 1 FROM space_conversation_members cm
                  WHERE cm.conversation_id=c.id AND cm.user_id=misty_rls_user_id()
              )
          )
    )
$$;

CREATE FUNCTION public.misty_is_space_member(candidate_space_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'on'
    AS $$
    SELECT EXISTS (
        SELECT 1 FROM space_members
        WHERE space_id=candidate_space_id AND user_id=misty_rls_user_id()
    )
$$;

CREATE FUNCTION public.misty_is_space_owner(candidate_space_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'on'
    AS $$
    SELECT EXISTS (
        SELECT 1 FROM spaces
        WHERE id=candidate_space_id AND owner_user_id=misty_rls_user_id()
    )
$$;

CREATE FUNCTION public.misty_note_permission_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
$$;

CREATE FUNCTION public.misty_notify_account_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE item jsonb; owner_id text; record_id text;
BEGIN
  IF TG_OP = 'DELETE' THEN item := to_jsonb(OLD); ELSE item := to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' AND TG_ARGV[0] IN ('runs','invocations','jobs')
     AND to_jsonb(OLD)->>'state' IS NOT DISTINCT FROM item->>'state'
     AND to_jsonb(OLD)->>'progress' IS NOT DISTINCT FROM item->>'progress' THEN RETURN NULL; END IF;
  IF TG_ARGV[0] = 'jobs' AND (TG_OP = 'DELETE' OR item ->> 'state' <> 'queued') THEN RETURN NULL; END IF;
  owner_id := item ->> TG_ARGV[1];
  record_id := COALESCE(item ->> 'id', item ->> 'agent_id');
  IF owner_id IS NOT NULL AND owner_id <> '' THEN
    PERFORM pg_notify('misty_account_events', json_build_object('userId', owner_id, 'topic', TG_ARGV[0], 'id', record_id)::text);
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION public.misty_pause_agent_execution_clock() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  -- Pausing is part of the same transaction as every wait/terminal transition,
  -- including transitions made by older services. Starting belongs to operation
  -- admission, so queued starts and undelivered resumes do not consume time.
  IF NEW.state<>'running' AND OLD.execution_active_at IS NOT NULL THEN
    NEW.execution_consumed_ms := LEAST(OLD.execution_limit_ms, OLD.execution_consumed_ms +
      GREATEST(0,CEIL(EXTRACT(EPOCH FROM (clock_timestamp()-OLD.execution_active_at))*1000)::bigint));
    NEW.execution_active_at := NULL;
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_protect_default_space() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF OLD.is_default AND EXISTS(SELECT 1 FROM public.users WHERE id=OLD.owner_user_id AND lifecycle_state='active') THEN
      RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.is_default AND (NOT NEW.is_default OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id) THEN
    RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514';
  END IF;
  IF OLD.is_default AND NEW.lifecycle_state IS DISTINCT FROM 'active' THEN
    IF NOT (public.misty_rls_is_service() AND OLD.lifecycle_state IN ('active','pending_deletion') AND NEW.lifecycle_state='pending_deletion'
      AND EXISTS(SELECT 1 FROM public.users u JOIN public.account_deletion_requests r ON r.user_id=u.id
        JOIN public.account_deletion_steps s ON s.request_id=r.id AND s.step='local'
        WHERE u.id=OLD.owner_user_id AND u.lifecycle_state='pending_deletion' AND r.cleanup_owner='native' AND r.status='processing'
          AND s.state='processing' AND s.lease_expires_at>clock_timestamp()
          AND EXISTS(SELECT 1 FROM public.account_deletion_steps p WHERE p.request_id=r.id AND p.step='payments' AND p.state='completed')
          AND EXISTS(SELECT 1 FROM public.account_deletion_steps p WHERE p.request_id=r.id AND p.step='providers' AND p.state='completed')))
    THEN RAISE EXCEPTION 'default space cannot be deleted or transferred' USING ERRCODE='23514'; END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION public.misty_rls_email() RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT NULLIF(current_setting('app.current_email', true), '')
$$;

CREATE FUNCTION public.misty_rls_is_service() RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT misty_rls_mode() = 'service'
$$;

CREATE FUNCTION public.misty_rls_license_id() RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT NULLIF(current_setting('app.current_license_id', true), '')
$$;

CREATE FUNCTION public.misty_rls_mode() RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT NULLIF(current_setting('app.rls_mode', true), '')
$$;

CREATE FUNCTION public.misty_rls_session_token_hash() RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT NULLIF(current_setting('app.current_session_token_hash', true), '')
$$;

CREATE FUNCTION public.misty_rls_user_id() RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT NULLIF(current_setting('app.current_user_id', true), '')
$$;

CREATE FUNCTION public.misty_routine_agent_pin_immutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF (NEW.user_id,NEW.invocation_id,NEW.step_id,NEW.call_namespace,NEW.model_id,NEW.max_turns)
  IS DISTINCT FROM (OLD.user_id,OLD.invocation_id,OLD.step_id,OLD.call_namespace,OLD.model_id,OLD.max_turns) THEN
  RAISE EXCEPTION 'routine agent admission is immutable';
 END IF;
 IF OLD.state NOT IN ('pending','running') AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'routine agent checkpoint is immutable';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION public.misty_routine_execution_immutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF OLD.execution IS DISTINCT FROM NEW.execution OR OLD.user_id<>NEW.user_id OR OLD.routine_id<>NEW.routine_id OR OLD.version<>NEW.version OR OLD.request_id<>NEW.request_id OR OLD.invocation_id<>NEW.invocation_id THEN
  RAISE EXCEPTION 'routine execution admission is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;

CREATE FUNCTION public.misty_routine_version_immutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN RAISE EXCEPTION 'routine versions are immutable' USING ERRCODE='23514'; END;
$$;

CREATE FUNCTION public.misty_routine_wait_immutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF (NEW.user_id,NEW.invocation_id,NEW.step_id,NEW.wait_id) IS DISTINCT FROM
    (OLD.user_id,OLD.invocation_id,OLD.step_id,OLD.wait_id) THEN
  RAISE EXCEPTION 'routine wait identity is immutable';
 END IF;
 IF OLD.state<>'pending' AND (NEW.until_at,NEW.expires_at) IS DISTINCT FROM (OLD.until_at,OLD.expires_at) THEN
  RAISE EXCEPTION 'routine wait deadline is immutable';
 END IF;
 IF OLD.state IN ('completed','expired','cancelled') AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'routine wait is terminal';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION public.preserve_agent_runtime_pin() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.runtime_adapter_version IS DISTINCT FROM OLD.runtime_adapter_version
    OR (OLD.runtime_endpoint IS NOT NULL AND NEW.runtime_endpoint IS DISTINCT FROM OLD.runtime_endpoint)
    OR (OLD.runtime_callback_endpoint IS NOT NULL AND NEW.runtime_callback_endpoint IS DISTINCT FROM OLD.runtime_callback_endpoint) THEN
    RAISE EXCEPTION 'agent_runtime_pin_immutable';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION public.queue_removed_ai_attachment_objects() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    SET "app.rls_mode" TO 'service'
    AS $$
DECLARE old_key TEXT; retained_keys TEXT[] := ARRAY[]::TEXT[]; owner_id TEXT;
BEGIN
  IF TG_OP='UPDATE' AND NEW.lifecycle_state<>'deleted' THEN
    retained_keys := ARRAY[NEW.object_key,NEW.model_object_key];
  END IF;
  SELECT id INTO owner_id FROM public.users WHERE id=OLD.user_id;
  FOR old_key IN SELECT DISTINCT unnest(ARRAY[OLD.object_key,OLD.model_object_key]) LOOP
    IF NOT(old_key=ANY(retained_keys)) THEN
      INSERT INTO public.object_deletion_jobs(object_key,not_before,created_by_user_id)
        VALUES(old_key,clock_timestamp()+interval '30 minutes',owner_id)
        ON CONFLICT(object_key) DO UPDATE SET not_before=GREATEST(object_deletion_jobs.not_before,EXCLUDED.not_before);
    END IF;
  END LOOP;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION public.queue_replaced_user_avatar() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    SET "app.rls_mode" TO 'service'
    AS $$
DECLARE old_key TEXT; next_key TEXT;
BEGIN
  IF OLD.avatar_version > 0 THEN
    old_key := COALESCE(OLD.avatar_object_key,'avatars/' || OLD.id);
    IF TG_OP = 'UPDATE' AND NEW.avatar_version > 0 THEN
      next_key := COALESCE(NEW.avatar_object_key,'avatars/' || NEW.id);
    END IF;
    IF old_key IS DISTINCT FROM next_key THEN
      INSERT INTO public.object_deletion_jobs(object_key,not_before,created_by_user_id) VALUES(old_key,now()+interval '5 minutes',CASE WHEN TG_OP='DELETE' THEN NULL ELSE OLD.id END)
        ON CONFLICT(object_key) DO UPDATE SET not_before=GREATEST(object_deletion_jobs.not_before,EXCLUDED.not_before);
    END IF;
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION public.refresh_owner_storage_usage(candidate_owner text) RETURNS void
    LANGUAGE plpgsql
    AS $$
BEGIN
    INSERT INTO owner_storage_usage(owner_user_id,used_bytes,reserved_bytes,version,updated_at)
    SELECT candidate_owner,
        COALESCE((SELECT SUM(su.used_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active'),0),
        COALESCE((SELECT SUM(su.reserved_bytes) FROM spaces s JOIN space_storage_usage su ON su.space_id=s.id
            WHERE s.owner_user_id=candidate_owner AND s.lifecycle_state='active'),0),
        1,NOW()
    ON CONFLICT(owner_user_id) DO UPDATE SET
        used_bytes=EXCLUDED.used_bytes,
        reserved_bytes=EXCLUDED.reserved_bytes,
        version=owner_storage_usage.version+1,
        updated_at=NOW();
END
$$;

CREATE FUNCTION public.sync_owner_storage_after_transfer() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.owner_user_id IS DISTINCT FROM NEW.owner_user_id THEN
        PERFORM refresh_owner_storage_usage(OLD.owner_user_id);
        PERFORM refresh_owner_storage_usage(NEW.owner_user_id);
    ELSIF OLD.lifecycle_state IS DISTINCT FROM NEW.lifecycle_state THEN
        PERFORM refresh_owner_storage_usage(NEW.owner_user_id);
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION public.sync_owner_storage_from_space_usage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE candidate_space TEXT; candidate_owner TEXT;
BEGIN
    candidate_space := COALESCE(NEW.space_id,OLD.space_id);
    SELECT owner_user_id INTO candidate_owner FROM spaces WHERE id=candidate_space;
    IF candidate_owner IS NOT NULL THEN PERFORM refresh_owner_storage_usage(candidate_owner); END IF;
    RETURN COALESCE(NEW,OLD);
END
$$;

CREATE TABLE public.abuse_blocks (
    block_key text NOT NULL,
    blocked_until timestamp with time zone NOT NULL,
    block_seconds integer DEFAULT 60 NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT abuse_blocks_block_key_check CHECK (((char_length(block_key) >= 1) AND (char_length(block_key) <= 200))),
    CONSTRAINT abuse_blocks_block_seconds_check CHECK ((block_seconds > 0)),
    CONSTRAINT abuse_blocks_reason_check CHECK ((char_length(reason) <= 120))
);

ALTER TABLE ONLY public.abuse_blocks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.account_deletion_provider_resources (
    request_id text NOT NULL,
    kind text NOT NULL,
    resource_id text NOT NULL,
    provider text NOT NULL,
    credential_kind text,
    credential_id text,
    credential_format text,
    ciphertext bytea,
    nonce bytea,
    key_version smallint,
    source_fingerprint text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    outcome text DEFAULT ''::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    execution_ciphertext bytea,
    execution_nonce bytea,
    execution_key_version smallint,
    execution_client_id_hash text,
    CONSTRAINT account_deletion_provider_execution_client CHECK (((execution_client_id_hash IS NULL) OR (execution_client_id_hash ~ '^[0-9a-f]{64}$'::text))),
    CONSTRAINT account_deletion_provider_execution_complete CHECK (((state <> 'completed'::text) OR ((execution_ciphertext IS NULL) AND (execution_nonce IS NULL)))),
    CONSTRAINT account_deletion_provider_execution_envelope CHECK ((((execution_ciphertext IS NULL) AND (execution_nonce IS NULL) AND (execution_key_version IS NULL)) OR ((execution_ciphertext IS NOT NULL) AND (execution_nonce IS NOT NULL) AND (execution_key_version IS NOT NULL) AND ((octet_length(execution_ciphertext) >= 16) AND (octet_length(execution_ciphertext) <= 2097168)) AND (octet_length(execution_nonce) = 12) AND (execution_key_version = 1) AND (execution_client_id_hash IS NOT NULL)))),
    CONSTRAINT account_deletion_provider_resources_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT account_deletion_provider_resources_check CHECK (((state = 'completed'::text) = (completed_at IS NOT NULL))),
    CONSTRAINT account_deletion_provider_resources_check1 CHECK (((credential_kind IS NULL) = (credential_id IS NULL))),
    CONSTRAINT account_deletion_provider_resources_check2 CHECK (((state <> 'completed'::text) OR ((ciphertext IS NULL) AND (nonce IS NULL)))),
    CONSTRAINT account_deletion_provider_resources_check3 CHECK (((ciphertext IS NULL) OR ((nonce IS NOT NULL) AND (key_version IS NOT NULL) AND (credential_format IS NOT NULL)))),
    CONSTRAINT account_deletion_provider_resources_ciphertext_check CHECK ((octet_length(ciphertext) <= 2097168)),
    CONSTRAINT account_deletion_provider_resources_credential_format_check CHECK ((credential_format = ANY (ARRAY['connected'::text, 'legacy'::text]))),
    CONSTRAINT account_deletion_provider_resources_credential_kind_check CHECK ((credential_kind = ANY (ARRAY['connected'::text, 'cloud'::text, 'integration'::text]))),
    CONSTRAINT account_deletion_provider_resources_details_check CHECK ((jsonb_typeof(details) = 'object'::text)),
    CONSTRAINT account_deletion_provider_resources_kind_check CHECK ((kind = ANY (ARRAY['connected'::text, 'cloud'::text, 'integration'::text, 'figma_webhook'::text, 'provider_subscription'::text]))),
    CONSTRAINT account_deletion_provider_resources_source_fingerprint_check CHECK ((source_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT account_deletion_provider_resources_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'completed'::text])))
);

ALTER TABLE ONLY public.account_deletion_provider_resources FORCE ROW LEVEL SECURITY;

CREATE TABLE public.account_deletion_requests (
    id text NOT NULL,
    user_id text NOT NULL,
    status text DEFAULT 'processing'::text NOT NULL,
    status_token_hash text NOT NULL,
    purge_after timestamp with time zone NOT NULL,
    provider_revocation_status jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    cleanup_owner text DEFAULT 'go'::text NOT NULL,
    CONSTRAINT account_deletion_requests_cleanup_owner_check CHECK ((cleanup_owner = ANY (ARRAY['go'::text, 'native'::text]))),
    CONSTRAINT account_deletion_requests_status_check CHECK ((status = ANY (ARRAY['processing'::text, 'scheduled'::text, 'completed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.account_deletion_requests FORCE ROW LEVEL SECURITY;

CREATE TABLE public.account_deletion_steps (
    request_id text NOT NULL,
    step text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_token uuid,
    lease_expires_at timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    result jsonb DEFAULT '{}'::jsonb NOT NULL,
    completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT account_deletion_steps_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT account_deletion_steps_check CHECK ((((state = 'processing'::text) AND (lease_token IS NOT NULL) AND (lease_expires_at IS NOT NULL)) OR ((state <> 'processing'::text) AND (lease_token IS NULL) AND (lease_expires_at IS NULL)))),
    CONSTRAINT account_deletion_steps_check1 CHECK (((state = 'completed'::text) = (completed_at IS NOT NULL))),
    CONSTRAINT account_deletion_steps_result_check CHECK ((jsonb_typeof(result) = 'object'::text)),
    CONSTRAINT account_deletion_steps_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'processing'::text, 'completed'::text]))),
    CONSTRAINT account_deletion_steps_step_check CHECK ((step = ANY (ARRAY['payments'::text, 'providers'::text, 'local'::text, 'purge'::text])))
);

ALTER TABLE ONLY public.account_deletion_steps FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_ask_conversation_events (
    id bigint NOT NULL,
    conversation_id text NOT NULL,
    user_id text NOT NULL,
    event_type text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_conversation_events_data_check CHECK ((jsonb_typeof(data) = 'object'::text)),
    CONSTRAINT agent_conversation_events_event_type_check CHECK ((event_type = ANY (ARRAY['user_message'::text, 'agent_message'::text, 'tool_call'::text, 'tool_result'::text, 'error'::text, 'assistant_message'::text])))
);

ALTER TABLE ONLY public.misty_ask_conversation_events FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.agent_conversation_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.agent_conversation_events_id_seq OWNED BY public.misty_ask_conversation_events.id;

CREATE TABLE public.agent_model_turn_claims (
    run_id text NOT NULL,
    user_id text NOT NULL,
    runtime_run_id text NOT NULL,
    node_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    space_run_id text GENERATED ALWAYS AS (
CASE
    WHEN ("left"(run_id, 11) = 'invocation_'::text) THEN NULL::text
    ELSE run_id
END) STORED,
    ai_invocation_id text GENERATED ALWAYS AS (
CASE
    WHEN ("left"(run_id, 11) = 'invocation_'::text) THEN run_id
    ELSE NULL::text
END) STORED,
    routine_usage jsonb,
    CONSTRAINT agent_model_turn_claims_node_id_check CHECK ((((length(node_id) >= 7) AND (length(node_id) <= 200)) AND (node_id ~~ 'model:%'::text)))
);

ALTER TABLE ONLY public.agent_model_turn_claims FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_run_contexts (
    id text NOT NULL,
    run_id text NOT NULL,
    owner_user_id text NOT NULL,
    space_id text,
    device_id text NOT NULL,
    kind text NOT NULL,
    opaque_ref text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    capabilities jsonb DEFAULT '[]'::jsonb NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'attached'::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_run_contexts_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'array'::text)),
    CONSTRAINT agent_run_contexts_kind_check CHECK ((kind = ANY (ARRAY['browser_tab'::text, 'project_root'::text]))),
    CONSTRAINT agent_run_contexts_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT agent_run_contexts_state_check CHECK ((state = ANY (ARRAY['attached'::text, 'detached'::text, 'expired'::text])))
);

ALTER TABLE ONLY public.agent_run_contexts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_run_jobs (
    run_id text NOT NULL,
    space_id text,
    task_id text,
    agent_id text NOT NULL,
    state text DEFAULT 'queued'::text NOT NULL,
    attempt integer DEFAULT 0 NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_owner text,
    lease_expires_at timestamp with time zone,
    last_error_code text DEFAULT ''::text NOT NULL,
    last_error_message text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    trigger_kind text DEFAULT 'task_assignment'::text NOT NULL,
    CONSTRAINT agent_run_jobs_trigger_kind_check CHECK ((trigger_kind = ANY (ARRAY['task_assignment'::text, 'direct_instruction'::text, 'creator_mention'::text, 'delegated'::text]))),
    CONSTRAINT personal_agent_task_run_jobs_attempt_check CHECK (((attempt >= 0) AND (attempt <= 3))),
    CONSTRAINT personal_agent_task_run_jobs_lease_check CHECK (((state = 'leased'::text) = ((lease_owner IS NOT NULL) AND (lease_expires_at IS NOT NULL)))),
    CONSTRAINT personal_agent_task_run_jobs_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'leased'::text, 'dispatched'::text, 'completed'::text, 'failed'::text, 'canceled'::text])))
);

ALTER TABLE ONLY public.agent_run_jobs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_run_tool_approvals (
    id text NOT NULL,
    run_id text,
    owner_user_id text NOT NULL,
    tool_call_id text NOT NULL,
    tool_name text NOT NULL,
    impact text NOT NULL,
    arguments_hash text NOT NULL,
    signed_call text NOT NULL,
    hook_token text NOT NULL,
    summary text DEFAULT ''::text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    decided_by_user_id text,
    expires_at timestamp with time zone DEFAULT (now() + '24:00:00'::interval) NOT NULL,
    decided_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    invocation_id text,
    sdk_effect_id uuid,
    sdk_review_digest text,
    sdk_review_ciphertext bytea,
    CONSTRAINT agent_run_tool_approvals_impact_check CHECK ((impact = ANY (ARRAY['routine'::text, 'consequential'::text, 'dangerous'::text]))),
    CONSTRAINT agent_run_tool_approvals_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'approved'::text, 'denied'::text, 'expired'::text]))),
    CONSTRAINT approval_run_identity CHECK (((((run_id IS NOT NULL))::integer + ((invocation_id IS NOT NULL))::integer) = 1)),
    CONSTRAINT sdk_approval_review_complete CHECK ((((sdk_effect_id IS NULL) AND (sdk_review_digest IS NULL) AND (sdk_review_ciphertext IS NULL)) OR ((sdk_effect_id IS NOT NULL) AND (sdk_review_digest IS NOT NULL) AND (sdk_review_digest ~ '^[0-9a-f]{64}$'::text) AND (sdk_review_ciphertext IS NOT NULL) AND ((octet_length(sdk_review_ciphertext) >= 32) AND (octet_length(sdk_review_ciphertext) <= 4194304)))))
);

ALTER TABLE ONLY public.agent_run_tool_approvals FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_runtime_deliveries (
    id text NOT NULL,
    user_id text NOT NULL,
    run_id text NOT NULL,
    operation text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_id text,
    lease_expires_at timestamp with time zone,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_runtime_deliveries_operation_check CHECK ((operation = ANY (ARRAY['invocation.start'::text, 'runtime.cancel'::text, 'runtime.reconcile'::text, 'approval.resume'::text, 'device.resume'::text, 'intervention.resume'::text]))),
    CONSTRAINT agent_runtime_deliveries_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'leased'::text, 'completed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.agent_runtime_deliveries FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_runtime_start_receipts (
    run_id text NOT NULL,
    claim_token text NOT NULL,
    runtime_run_id text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.agent_runtime_start_receipts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_sdk_capability_bindings (
    run_id text NOT NULL,
    user_id text NOT NULL,
    target_id uuid NOT NULL,
    target_revision integer NOT NULL,
    capability text NOT NULL,
    capability_version integer NOT NULL,
    provider_id text NOT NULL,
    provider_version integer NOT NULL,
    adapter_version text DEFAULT 'sdk-backend-v1'::text NOT NULL,
    space_run_id text GENERATED ALWAYS AS (
CASE
    WHEN ("left"(run_id, 11) = 'invocation_'::text) THEN NULL::text
    ELSE run_id
END) STORED,
    ai_invocation_id text GENERATED ALWAYS AS (
CASE
    WHEN ("left"(run_id, 11) = 'invocation_'::text) THEN run_id
    ELSE NULL::text
END) STORED,
    CONSTRAINT agent_sdk_capability_bindings_capability_version_check CHECK ((capability_version > 0)),
    CONSTRAINT agent_sdk_capability_bindings_provider_version_check CHECK ((provider_version > 0))
);

ALTER TABLE ONLY public.agent_sdk_capability_bindings FORCE ROW LEVEL SECURITY;

CREATE TABLE public.agent_toolbox_action_journal (
    idempotency_key text NOT NULL,
    user_id text NOT NULL,
    space_id text,
    agent_id text,
    agent_instance_id text,
    run_id text,
    session_id text,
    tool_name text NOT NULL,
    audit_event text NOT NULL,
    risk text NOT NULL,
    source text NOT NULL,
    request jsonb DEFAULT '{}'::jsonb NOT NULL,
    result jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text NOT NULL,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    request_fingerprint text,
    result_ciphertext bytea,
    CONSTRAINT agent_toolbox_action_journal_request_check CHECK ((jsonb_typeof(request) = 'object'::text)),
    CONSTRAINT agent_toolbox_action_journal_risk_check CHECK ((risk = ANY (ARRAY['read'::text, 'write'::text, 'dangerous'::text]))),
    CONSTRAINT agent_toolbox_action_journal_state_check CHECK ((state = ANY (ARRAY['started'::text, 'completed'::text, 'failed'::text, 'unknown'::text])))
);

ALTER TABLE ONLY public.agent_toolbox_action_journal FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_artifacts (
    id text NOT NULL,
    invocation_id text NOT NULL,
    user_id text NOT NULL,
    schema_version integer NOT NULL,
    kind text NOT NULL,
    title text NOT NULL,
    summary text DEFAULT ''::text NOT NULL,
    sources jsonb DEFAULT '[]'::jsonb NOT NULL,
    target jsonb,
    base_revision jsonb,
    operations jsonb DEFAULT '{}'::jsonb NOT NULL,
    risk text NOT NULL,
    approval_policy text NOT NULL,
    idempotency_key text NOT NULL,
    state text NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    decided_at timestamp with time zone,
    completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_artifacts_approval_policy_check CHECK ((approval_policy = ANY (ARRAY['none'::text, 'auto_apply_with_undo'::text, 'visible_apply'::text, 'confirm'::text, 'always_confirm'::text]))),
    CONSTRAINT ai_artifacts_kind_check CHECK ((kind = ANY (ARRAY['text_patch'::text, 'task_set'::text, 'calendar_event'::text, 'roadmap_patch'::text, 'drawing_patch'::text, 'file_plan'::text, 'mail_draft'::text, 'message_draft'::text, 'code_patch'::text, 'terminal_command'::text, 'browser_action'::text, 'transfer_plan'::text, 'extension_action'::text, 'image_edit'::text]))),
    CONSTRAINT ai_artifacts_risk_check CHECK ((risk = ANY (ARRAY['observe'::text, 'draft'::text, 'consequential'::text, 'dangerous'::text]))),
    CONSTRAINT ai_artifacts_schema_version_check CHECK ((schema_version > 0)),
    CONSTRAINT ai_artifacts_sources_check CHECK ((jsonb_typeof(sources) = 'array'::text)),
    CONSTRAINT ai_artifacts_state_check CHECK ((state = ANY (ARRAY['proposed'::text, 'applying'::text, 'applied'::text, 'rejected'::text, 'stale'::text, 'failed'::text]))),
    CONSTRAINT ai_artifacts_target_check CHECK (((target IS NULL) OR (jsonb_typeof(target) = 'object'::text)))
);

ALTER TABLE ONLY public.ai_artifacts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_cleanup_jobs (
    id text NOT NULL,
    user_id text NOT NULL,
    state text DEFAULT 'queued'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_cleanup_jobs_attempts_check CHECK (((attempts >= 0) AND (attempts <= 20))),
    CONSTRAINT ai_cleanup_jobs_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'working'::text, 'verified'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.ai_cleanup_jobs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_conversation_attachments (
    id text NOT NULL,
    user_id text NOT NULL,
    conversation_id text,
    invocation_id text,
    scope text NOT NULL,
    display_name text NOT NULL,
    mime_type text NOT NULL,
    byte_size bigint NOT NULL,
    sha256 text NOT NULL,
    width integer NOT NULL,
    height integer NOT NULL,
    object_key text NOT NULL,
    model_mime_type text NOT NULL,
    model_byte_size bigint NOT NULL,
    model_sha256 text NOT NULL,
    model_width integer NOT NULL,
    model_height integer NOT NULL,
    model_object_key text NOT NULL,
    lifecycle_state text DEFAULT 'pending'::text NOT NULL,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_conversation_attachments_byte_size_check CHECK (((byte_size >= 1) AND (byte_size <= 10485760))),
    CONSTRAINT ai_conversation_attachments_check CHECK ((((scope = 'conversation'::text) AND (conversation_id IS NOT NULL)) OR ((scope = 'visual_query'::text) AND (conversation_id IS NULL) AND (expires_at IS NOT NULL)))),
    CONSTRAINT ai_conversation_attachments_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
    CONSTRAINT ai_conversation_attachments_height_check CHECK (((height >= 1) AND (height <= 16384))),
    CONSTRAINT ai_conversation_attachments_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['pending'::text, 'ready'::text, 'deleted'::text]))),
    CONSTRAINT ai_conversation_attachments_mime_type_check CHECK ((mime_type = ANY (ARRAY['image/jpeg'::text, 'image/png'::text, 'image/webp'::text, 'application/pdf'::text, 'text/plain'::text, 'text/markdown'::text, 'text/csv'::text, 'application/json'::text, 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'::text]))),
    CONSTRAINT ai_conversation_attachments_model_byte_size_check CHECK (((model_byte_size >= 1) AND (model_byte_size <= 10485760))),
    CONSTRAINT ai_conversation_attachments_model_height_check CHECK (((model_height >= 1) AND (model_height <= 2048))),
    CONSTRAINT ai_conversation_attachments_model_mime_type_check CHECK ((model_mime_type = ANY (ARRAY['image/jpeg'::text, 'image/png'::text, 'image/webp'::text, 'application/pdf'::text, 'text/plain'::text]))),
    CONSTRAINT ai_conversation_attachments_model_sha256_check CHECK ((model_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT ai_conversation_attachments_model_width_check CHECK (((model_width >= 1) AND (model_width <= 2048))),
    CONSTRAINT ai_conversation_attachments_scope_check CHECK ((scope = ANY (ARRAY['conversation'::text, 'visual_query'::text]))),
    CONSTRAINT ai_conversation_attachments_sha256_check CHECK ((sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT ai_conversation_attachments_width_check CHECK (((width >= 1) AND (width <= 16384)))
);

ALTER TABLE ONLY public.ai_conversation_attachments FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_feature_flags (
    surface_id text DEFAULT '*'::text NOT NULL,
    action_id text DEFAULT '*'::text NOT NULL,
    model_id text DEFAULT '*'::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    rollout_percent smallint DEFAULT 100 NOT NULL,
    updated_by_user_id text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_feature_flags_rollout_percent_check CHECK (((rollout_percent >= 0) AND (rollout_percent <= 100)))
);

ALTER TABLE ONLY public.ai_feature_flags FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_feedback (
    id text NOT NULL,
    user_id text NOT NULL,
    invocation_id text NOT NULL,
    rating smallint NOT NULL,
    reason_code text DEFAULT ''::text NOT NULL,
    comment text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_feedback_comment_check CHECK ((char_length(comment) <= 2000)),
    CONSTRAINT ai_feedback_rating_check CHECK ((rating = ANY (ARRAY['-1'::integer, 1])))
);

ALTER TABLE ONLY public.ai_feedback FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_intervention_waits (
    id text NOT NULL,
    user_id text NOT NULL,
    invocation_id text,
    runtime_run_id text NOT NULL,
    call_id text NOT NULL,
    arguments_hash text NOT NULL,
    hook_token text NOT NULL,
    context_id text NOT NULL,
    device_id text NOT NULL,
    scope_id text NOT NULL,
    target_label text NOT NULL,
    action text NOT NULL,
    reason text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    consumed_at timestamp with time zone,
    space_run_id text,
    run_id text GENERATED ALWAYS AS (COALESCE(invocation_id, space_run_id)) STORED,
    CONSTRAINT ai_intervention_waits_action_check CHECK ((action = ANY (ARRAY['sign_in'::text, 'account_confirmation'::text, 'challenge'::text, 'open_target'::text, 'review'::text]))),
    CONSTRAINT ai_intervention_waits_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'ready'::text, 'declined'::text, 'expired'::text]))),
    CONSTRAINT intervention_one_run CHECK ((num_nonnulls(invocation_id, space_run_id) = 1))
);

ALTER TABLE ONLY public.ai_intervention_waits FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_invocation_contexts (
    id text NOT NULL,
    invocation_id text NOT NULL,
    user_id text NOT NULL,
    space_id text,
    device_id text NOT NULL,
    kind text NOT NULL,
    opaque_ref text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    capabilities jsonb DEFAULT '[]'::jsonb NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'attached'::text NOT NULL,
    expires_at timestamp with time zone DEFAULT (now() + '00:30:00'::interval) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_invocation_contexts_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'array'::text)),
    CONSTRAINT ai_invocation_contexts_display_name_check CHECK ((char_length(display_name) <= 255)),
    CONSTRAINT ai_invocation_contexts_kind_check CHECK ((kind = 'browser_tab'::text)),
    CONSTRAINT ai_invocation_contexts_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT ai_invocation_contexts_opaque_ref_check CHECK (((char_length(opaque_ref) >= 1) AND (char_length(opaque_ref) <= 512))),
    CONSTRAINT ai_invocation_contexts_state_check CHECK ((state = ANY (ARRAY['attached'::text, 'detached'::text, 'expired'::text])))
);

ALTER TABLE ONLY public.ai_invocation_contexts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_invocation_events (
    invocation_id text NOT NULL,
    sequence bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    native_resulting_state text,
    receipt_key text,
    CONSTRAINT ai_invocation_events_event_type_check CHECK (((char_length(event_type) >= 1) AND (char_length(event_type) <= 80))),
    CONSTRAINT ai_invocation_events_native_resulting_state_check CHECK ((native_resulting_state = ANY (ARRAY['running'::text, 'awaiting_approval'::text, 'completed'::text, 'failed'::text, 'canceled'::text]))),
    CONSTRAINT ai_invocation_events_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text)),
    CONSTRAINT ai_invocation_events_sequence_check CHECK ((sequence > 0))
);

ALTER TABLE ONLY public.ai_invocation_events FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_invocations (
    id text NOT NULL,
    user_id text NOT NULL,
    conversation_id text,
    surface_id text NOT NULL,
    mode text NOT NULL,
    trigger_kind text NOT NULL,
    state text NOT NULL,
    idempotency_key text NOT NULL,
    request_payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone,
    canceled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    runtime_kind text DEFAULT ''::text NOT NULL,
    runtime_run_id text DEFAULT ''::text NOT NULL,
    agent_run_id text,
    runtime_heartbeat_at timestamp with time zone,
    space_id text,
    runtime_owner text DEFAULT 'go'::text NOT NULL,
    approval_wait_id text DEFAULT ''::text NOT NULL,
    model_budget_version integer DEFAULT 1 NOT NULL,
    model_turn_limit integer DEFAULT 20 NOT NULL,
    execution_budget_version integer DEFAULT 1 NOT NULL,
    execution_limit_ms bigint DEFAULT 1800000 NOT NULL,
    execution_consumed_ms bigint DEFAULT 0 NOT NULL,
    execution_active_at timestamp with time zone,
    runtime_adapter_version text DEFAULT 'vercel-workflow/1'::text NOT NULL,
    runtime_endpoint text,
    runtime_callback_endpoint text,
    device_wait_hook_token text DEFAULT ''::text NOT NULL,
    device_wait_expires_at timestamp with time zone,
    device_wait_context_id text DEFAULT ''::text NOT NULL,
    device_wait_scope_id text DEFAULT ''::text NOT NULL,
    device_wait_capability text DEFAULT ''::text NOT NULL,
    device_wait_call_id text DEFAULT ''::text NOT NULL,
    device_wait_arguments_hash text DEFAULT ''::text NOT NULL,
    runtime_observed_at timestamp with time zone,
    runtime_observed_status text DEFAULT ''::text NOT NULL,
    CONSTRAINT ai_invocations_execution_budget_version_check CHECK ((execution_budget_version = ANY (ARRAY[0, 1]))),
    CONSTRAINT ai_invocations_execution_consumed_ms_check CHECK ((execution_consumed_ms >= 0)),
    CONSTRAINT ai_invocations_execution_limit_ms_check CHECK (((execution_limit_ms >= 1) AND (execution_limit_ms <= 1800000))),
    CONSTRAINT ai_invocations_idempotency_key_check CHECK (((char_length(idempotency_key) >= 1) AND (char_length(idempotency_key) <= 200))),
    CONSTRAINT ai_invocations_mode_check CHECK ((mode = ANY (ARRAY['quick'::text, 'drawer'::text, 'companion'::text]))),
    CONSTRAINT ai_invocations_model_budget_version_check CHECK ((model_budget_version = ANY (ARRAY[0, 1]))),
    CONSTRAINT ai_invocations_model_turn_limit_check CHECK (((model_turn_limit >= 1) AND (model_turn_limit <= 120))),
    CONSTRAINT ai_invocations_request_payload_check CHECK ((jsonb_typeof(request_payload) = 'object'::text)),
    CONSTRAINT ai_invocations_runtime_owner_check CHECK ((runtime_owner = ANY (ARRAY['go'::text, 'hono'::text]))),
    CONSTRAINT ai_invocations_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'awaiting_approval'::text, 'awaiting_device'::text, 'awaiting_intervention'::text, 'awaiting_timer'::text, 'completed'::text, 'failed'::text, 'canceled'::text]))),
    CONSTRAINT ai_invocations_surface_id_check CHECK (((char_length(surface_id) >= 1) AND (char_length(surface_id) <= 80))),
    CONSTRAINT ai_invocations_trigger_kind_check CHECK ((trigger_kind = ANY (ARRAY['message'::text, 'selection'::text, 'object'::text, 'schedule'::text, 'event'::text, 'handoff'::text])))
);

ALTER TABLE ONLY public.ai_invocations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_recaps (
    user_id text NOT NULL,
    surface_id text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    cadence text DEFAULT 'daily'::text NOT NULL,
    local_time text DEFAULT '08:00'::text NOT NULL,
    weekday smallint DEFAULT 1 NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    prompt text DEFAULT ''::text NOT NULL,
    state text DEFAULT 'idle'::text NOT NULL,
    next_run_at timestamp with time zone,
    lease_until timestamp with time zone,
    last_invocation_id text,
    last_result text DEFAULT ''::text NOT NULL,
    last_citations jsonb DEFAULT '[]'::jsonb NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    last_run_at timestamp with time zone,
    last_seen_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_recaps_cadence_check CHECK ((cadence = ANY (ARRAY['daily'::text, 'weekly'::text]))),
    CONSTRAINT ai_recaps_last_citations_check CHECK ((jsonb_typeof(last_citations) = 'array'::text)),
    CONSTRAINT ai_recaps_last_error_check CHECK ((char_length(last_error) <= 2000)),
    CONSTRAINT ai_recaps_last_result_check CHECK ((char_length(last_result) <= 100000)),
    CONSTRAINT ai_recaps_local_time_check CHECK ((local_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'::text)),
    CONSTRAINT ai_recaps_prompt_check CHECK ((char_length(prompt) <= 8000)),
    CONSTRAINT ai_recaps_state_check CHECK ((state = ANY (ARRAY['idle'::text, 'running'::text, 'failed'::text]))),
    CONSTRAINT ai_recaps_surface_id_check CHECK (((char_length(surface_id) >= 1) AND (char_length(surface_id) <= 80))),
    CONSTRAINT ai_recaps_timezone_check CHECK (((char_length(timezone) >= 1) AND (char_length(timezone) <= 100))),
    CONSTRAINT ai_recaps_weekday_check CHECK (((weekday >= 0) AND (weekday <= 6)))
);

ALTER TABLE ONLY public.ai_recaps FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_retrieval_chunks (
    document_id text NOT NULL,
    ordinal integer NOT NULL,
    content text NOT NULL,
    content_hash text NOT NULL,
    lexical tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, content)) STORED,
    embedding public.vector(768),
    embedding_model text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    embedding_attempt_id uuid,
    embedding_lease_until timestamp with time zone,
    CONSTRAINT ai_retrieval_chunks_content_check CHECK ((char_length(content) <= 16000)),
    CONSTRAINT ai_retrieval_chunks_ordinal_check CHECK ((ordinal >= 0))
);

ALTER TABLE ONLY public.ai_retrieval_chunks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_retrieval_documents (
    id text NOT NULL,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    owner_user_id text,
    space_id text,
    audience_kind text,
    audience_conversation_id text,
    privacy_class text NOT NULL,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    source_revision text NOT NULL,
    title text NOT NULL,
    href text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_retrieval_documents_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT ai_retrieval_documents_check CHECK ((((privacy_class = 'private'::text) AND (owner_user_id IS NOT NULL) AND (space_id IS NULL) AND (audience_kind IS NULL)) OR ((privacy_class = ANY (ARRAY['shared'::text, 'provider'::text])) AND (owner_user_id IS NULL) AND (space_id IS NOT NULL) AND (audience_kind IS NOT NULL)))),
    CONSTRAINT ai_retrieval_documents_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'deleted'::text, 'inaccessible'::text]))),
    CONSTRAINT ai_retrieval_documents_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT ai_retrieval_documents_privacy_class_check CHECK ((privacy_class = ANY (ARRAY['private'::text, 'shared'::text, 'provider'::text])))
);

ALTER TABLE ONLY public.ai_retrieval_documents FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_runtime_callback_receipts (
    invocation_id text NOT NULL,
    effect_key text NOT NULL,
    body_sha256 text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ai_runtime_callback_receipts_body_sha256_check CHECK ((body_sha256 ~ '^[a-f0-9]{64}$'::text))
);

ALTER TABLE ONLY public.ai_runtime_callback_receipts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_surface_preferences (
    user_id text NOT NULL,
    surface_id text NOT NULL,
    proactive_enabled boolean DEFAULT false NOT NULL,
    saved_actions jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    proactive_cooldown_minutes smallint DEFAULT 360 NOT NULL,
    proactive_snoozed_until timestamp with time zone,
    proactive_last_shown_at timestamp with time zone,
    proactive_dismissed_at timestamp with time zone,
    CONSTRAINT ai_surface_preferences_proactive_cooldown_minutes_check CHECK (((proactive_cooldown_minutes >= 30) AND (proactive_cooldown_minutes <= 10080))),
    CONSTRAINT ai_surface_preferences_saved_actions_check CHECK ((jsonb_typeof(saved_actions) = 'array'::text))
);

ALTER TABLE ONLY public.ai_surface_preferences FORCE ROW LEVEL SECURITY;

CREATE TABLE public.ai_user_settings (
    user_id text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    retention_days smallint DEFAULT 30 NOT NULL,
    purge_state text DEFAULT 'none'::text NOT NULL,
    disabled_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    cursor_companion_enabled boolean DEFAULT true NOT NULL,
    memory_enabled boolean DEFAULT true NOT NULL,
    CONSTRAINT ai_user_settings_purge_state_check CHECK ((purge_state = ANY (ARRAY['none'::text, 'queued'::text, 'working'::text, 'verified'::text, 'failed'::text]))),
    CONSTRAINT ai_user_settings_retention_days_check CHECK (((retention_days >= 1) AND (retention_days <= 365)))
);

ALTER TABLE ONLY public.ai_user_settings FORCE ROW LEVEL SECURITY;

CREATE TABLE public.auth_handoff_tokens (
    hashed_token character varying(64) NOT NULL,
    user_id text NOT NULL,
    redirect_path text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT auth_handoff_tokens_hashed_token_check CHECK ((char_length((hashed_token)::text) = 64))
);

ALTER TABLE ONLY public.auth_handoff_tokens FORCE ROW LEVEL SECURITY;

CREATE TABLE public.billing_adapter_intents (
    account_id text NOT NULL,
    key text NOT NULL,
    admission jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    expires_at timestamp with time zone DEFAULT (now() + '00:02:00'::interval) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT billing_adapter_intents_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'admitted'::text, 'abandoned'::text, 'recovered'::text])))
);

CREATE TABLE public.billing_adapter_outbox (
    id text NOT NULL,
    action text NOT NULL,
    payload jsonb NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    delivered_at timestamp with time zone,
    CONSTRAINT billing_adapter_outbox_action_check CHECK ((action = ANY (ARRAY['settle'::text, 'release'::text, 'refund'::text, 'settle_group'::text, 'release_group'::text, 'close'::text])))
);

CREATE TABLE public.billing_adapter_reservations (
    account_id text NOT NULL,
    key text NOT NULL,
    reservation_id text NOT NULL,
    admission jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.browser_sync_checkpoints (
    workspace_id uuid NOT NULL,
    sequence bigint NOT NULL,
    key_epoch bigint NOT NULL,
    device_id uuid NOT NULL,
    envelope jsonb NOT NULL,
    signature bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT browser_sync_checkpoints_envelope_check CHECK ((jsonb_typeof(envelope) = 'object'::text)),
    CONSTRAINT browser_sync_checkpoints_key_epoch_check CHECK ((key_epoch > 0)),
    CONSTRAINT browser_sync_checkpoints_sequence_check CHECK ((sequence > 0)),
    CONSTRAINT browser_sync_checkpoints_signature_check CHECK ((octet_length(signature) = 64))
);

CREATE TABLE public.browser_sync_connections (
    connection_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    device_id uuid NOT NULL,
    ready boolean DEFAULT false NOT NULL,
    applied_sequence bigint DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone DEFAULT (clock_timestamp() + '00:00:45'::interval) NOT NULL,
    last_seen_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT browser_sync_connections_applied_sequence_check CHECK ((applied_sequence >= 0))
);

CREATE TABLE public.browser_sync_control_requests (
    workspace_id uuid NOT NULL,
    device_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    expires_at timestamp with time zone DEFAULT (clock_timestamp() + '00:00:30'::interval) NOT NULL
);

CREATE TABLE public.browser_sync_devices (
    workspace_id uuid NOT NULL,
    device_id uuid NOT NULL,
    public_key bytea NOT NULL,
    grant_epoch bigint NOT NULL,
    grant_signature bytea NOT NULL,
    last_counter bigint DEFAULT 0 NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    platform text DEFAULT ''::text NOT NULL,
    control_version integer DEFAULT 0 NOT NULL,
    full_sync boolean DEFAULT true NOT NULL,
    activation_request uuid,
    activation_expires_at timestamp with time zone,
    CONSTRAINT browser_sync_devices_grant_epoch_check CHECK ((grant_epoch > 0)),
    CONSTRAINT browser_sync_devices_grant_signature_check CHECK ((octet_length(grant_signature) = 64)),
    CONSTRAINT browser_sync_devices_last_counter_check CHECK ((last_counter >= 0)),
    CONSTRAINT browser_sync_devices_public_key_check CHECK ((octet_length(public_key) = 32))
);

COMMENT ON TABLE public.browser_sync_devices IS 'Workspace-authorized signing identities. Connection presence never grants write authority.';

CREATE TABLE public.browser_sync_events (
    workspace_id uuid NOT NULL,
    sequence bigint NOT NULL,
    operation_id uuid NOT NULL,
    device_id uuid NOT NULL,
    device_counter bigint NOT NULL,
    key_epoch bigint NOT NULL,
    envelope jsonb NOT NULL,
    signature bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT browser_sync_events_device_counter_check CHECK ((device_counter > 0)),
    CONSTRAINT browser_sync_events_envelope_check CHECK ((jsonb_typeof(envelope) = 'object'::text)),
    CONSTRAINT browser_sync_events_key_epoch_check CHECK ((key_epoch > 0)),
    CONSTRAINT browser_sync_events_sequence_check CHECK ((sequence > 0)),
    CONSTRAINT browser_sync_events_signature_check CHECK ((octet_length(signature) = 64))
);

COMMENT ON TABLE public.browser_sync_events IS 'Opaque endpoint-encrypted changes, ordered by committed server sequence. No decryption keys or browser plaintext.';

CREATE TABLE public.browser_sync_receipts (
    workspace_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    sequence bigint NOT NULL,
    device_id uuid NOT NULL,
    device_counter bigint NOT NULL,
    content_hash bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    discarded boolean DEFAULT false NOT NULL,
    CONSTRAINT browser_sync_receipts_content_hash_check CHECK ((octet_length(content_hash) = 32))
);

CREATE TABLE public.browser_sync_tickets (
    token_hash text NOT NULL,
    workspace_id uuid NOT NULL,
    device_id uuid NOT NULL,
    expires_at timestamp with time zone DEFAULT (clock_timestamp() + '00:01:00'::interval) NOT NULL
);

CREATE TABLE public.browser_sync_workspaces (
    user_id text NOT NULL,
    workspace_id uuid NOT NULL,
    protocol_version integer DEFAULT 1 NOT NULL,
    key_epoch bigint DEFAULT 1 NOT NULL,
    head_sequence bigint DEFAULT 0 NOT NULL,
    root_public_key bytea NOT NULL,
    key_envelope jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    active_device_id uuid,
    active_epoch uuid,
    active_seen_at timestamp with time zone,
    CONSTRAINT browser_sync_active_pair CHECK (((active_device_id IS NULL) = (active_epoch IS NULL))),
    CONSTRAINT browser_sync_workspaces_head_sequence_check CHECK ((head_sequence >= 0)),
    CONSTRAINT browser_sync_workspaces_key_envelope_check CHECK ((jsonb_typeof(key_envelope) = 'object'::text)),
    CONSTRAINT browser_sync_workspaces_key_epoch_check CHECK ((key_epoch > 0)),
    CONSTRAINT browser_sync_workspaces_protocol_version_check CHECK ((protocol_version = 1)),
    CONSTRAINT browser_sync_workspaces_root_public_key_check CHECK ((octet_length(root_public_key) = 32))
);

COMMENT ON COLUMN public.browser_sync_workspaces.active_epoch IS 'Signed takeover operation ID. Periodic heartbeats refresh liveness but never claim control.';

CREATE TABLE public.cloud_connections (
    id text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    name text NOT NULL,
    account_id text NOT NULL,
    account_display text DEFAULT ''::text NOT NULL,
    credential_ciphertext bytea NOT NULL,
    credential_nonce bytea NOT NULL,
    key_version smallint DEFAULT 1 NOT NULL,
    uses_custom_oauth_client boolean DEFAULT false NOT NULL,
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    connected_account_id text,
    status text DEFAULT 'active'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    CONSTRAINT cloud_connections_provider_check CHECK ((provider = ANY (ARRAY['drive'::text, 'dropbox'::text, 'onedrive'::text]))),
    CONSTRAINT cloud_connections_status_check CHECK ((status = ANY (ARRAY['active'::text, 'needs_attention'::text, 'revoked'::text])))
);

ALTER TABLE ONLY public.cloud_connections FORCE ROW LEVEL SECURITY;

CREATE TABLE public.cloud_credential_handoffs (
    handoff_hash text NOT NULL,
    user_id text NOT NULL,
    cloud_connection_id text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cloud_credential_handoffs_handoff_hash_check CHECK ((handoff_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.cloud_credential_handoffs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.cloud_oauth_states (
    state_hash text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    connection_name text NOT NULL,
    secret_ciphertext bytea NOT NULL,
    secret_nonce bytea NOT NULL,
    return_to text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cloud_oauth_states_provider_check CHECK ((provider = ANY (ARRAY['drive'::text, 'dropbox'::text, 'onedrive'::text]))),
    CONSTRAINT cloud_oauth_states_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.cloud_oauth_states FORCE ROW LEVEL SECURITY;

CREATE TABLE public.connected_account_oauth_states (
    state_hash text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    capabilities jsonb NOT NULL,
    requested_scopes jsonb NOT NULL,
    verifier_ciphertext bytea NOT NULL,
    verifier_nonce bytea NOT NULL,
    return_to text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT connected_account_oauth_states_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'array'::text)),
    CONSTRAINT connected_account_oauth_states_provider_check CHECK ((provider ~ '^[a-z][a-z0-9_]{1,31}$'::text)),
    CONSTRAINT connected_account_oauth_states_requested_scopes_check CHECK ((jsonb_typeof(requested_scopes) = 'array'::text)),
    CONSTRAINT connected_account_oauth_states_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.connected_account_oauth_states FORCE ROW LEVEL SECURITY;

CREATE TABLE public.connected_accounts (
    id text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    account_id text NOT NULL,
    account_display text DEFAULT ''::text NOT NULL,
    credential_ciphertext bytea NOT NULL,
    credential_nonce bytea NOT NULL,
    key_version smallint DEFAULT 1 NOT NULL,
    capabilities jsonb DEFAULT '[]'::jsonb NOT NULL,
    granted_scopes jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone,
    last_refreshed_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT connected_accounts_account_display_check CHECK ((char_length(account_display) <= 320)),
    CONSTRAINT connected_accounts_account_id_check CHECK (((char_length(account_id) >= 1) AND (char_length(account_id) <= 320))),
    CONSTRAINT connected_accounts_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'array'::text)),
    CONSTRAINT connected_accounts_granted_scopes_check CHECK ((jsonb_typeof(granted_scopes) = 'array'::text)),
    CONSTRAINT connected_accounts_id_check CHECK ((id ~ '^connection_[0-9a-f-]{36}$'::text)),
    CONSTRAINT connected_accounts_provider_check CHECK ((provider ~ '^[a-z][a-z0-9_]{1,31}$'::text)),
    CONSTRAINT connected_accounts_status_check CHECK ((status = ANY (ARRAY['active'::text, 'needs_attention'::text, 'revoked'::text])))
);

ALTER TABLE ONLY public.connected_accounts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.connection_authorization_requests (
    state_hash text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    actor jsonb NOT NULL,
    credential_snapshot jsonb NOT NULL,
    capabilities jsonb NOT NULL,
    requested_scopes jsonb NOT NULL,
    verifier_ciphertext bytea NOT NULL,
    verifier_nonce bytea NOT NULL,
    redirect_uri text NOT NULL,
    client_id_hash text NOT NULL,
    return_to text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT connection_authorization_requests_actor_check CHECK ((jsonb_typeof(actor) = 'object'::text)),
    CONSTRAINT connection_authorization_requests_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'array'::text)),
    CONSTRAINT connection_authorization_requests_client_id_hash_check CHECK ((client_id_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT connection_authorization_requests_credential_snapshot_check CHECK ((jsonb_typeof(credential_snapshot) = 'array'::text)),
    CONSTRAINT connection_authorization_requests_provider_check CHECK ((provider = ANY (ARRAY['google'::text, 'microsoft'::text, 'dropbox'::text, 'figma'::text, 'discord'::text, 'instagram'::text]))),
    CONSTRAINT connection_authorization_requests_requested_scopes_check CHECK ((jsonb_typeof(requested_scopes) = 'array'::text)),
    CONSTRAINT connection_authorization_requests_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.connection_authorization_requests FORCE ROW LEVEL SECURITY;

CREATE TABLE public.device_pairing_sessions (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    creator_device_id text NOT NULL,
    requester_device_id text,
    qr_secret_hash text NOT NULL,
    manual_code_hash text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    failed_attempts integer DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    redeemed_at timestamp with time zone,
    confirmed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_pairing_sessions_check CHECK ((expires_at <= (created_at + '00:05:05'::interval))),
    CONSTRAINT device_pairing_sessions_failed_attempts_check CHECK (((failed_attempts >= 0) AND (failed_attempts <= 5))),
    CONSTRAINT device_pairing_sessions_id_check CHECK ((id ~ '^pairing_[0-9a-f-]{36}$'::text)),
    CONSTRAINT device_pairing_sessions_manual_code_hash_check CHECK ((manual_code_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT device_pairing_sessions_qr_secret_hash_check CHECK ((qr_secret_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT device_pairing_sessions_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'redeemed'::text, 'confirmed'::text, 'expired'::text, 'locked'::text])))
);

ALTER TABLE ONLY public.device_pairing_sessions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.device_pairs (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    first_device_id text NOT NULL,
    second_device_id text NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    clipboard_first_to_second boolean DEFAULT false NOT NULL,
    clipboard_second_to_first boolean DEFAULT false NOT NULL,
    first_peer_name text,
    second_peer_name text,
    confirmed_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_pairs_check CHECK ((first_device_id < second_device_id)),
    CONSTRAINT device_pairs_first_peer_name_check CHECK (((first_peer_name IS NULL) OR ((length(first_peer_name) >= 1) AND (length(first_peer_name) <= 80)))),
    CONSTRAINT device_pairs_id_check CHECK ((id ~ '^pair_[0-9a-f-]{36}$'::text)),
    CONSTRAINT device_pairs_second_peer_name_check CHECK (((second_peer_name IS NULL) OR ((length(second_peer_name) >= 1) AND (length(second_peer_name) <= 80)))),
    CONSTRAINT device_pairs_state_check CHECK ((state = ANY (ARRAY['active'::text, 'revoked'::text])))
);

ALTER TABLE ONLY public.device_pairs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.device_presence (
    device_id text NOT NULL,
    owner_user_id text NOT NULL,
    p2p_endpoint_id text NOT NULL,
    addressing jsonb DEFAULT '{}'::jsonb NOT NULL,
    protocol_version text NOT NULL,
    connection_hint text DEFAULT 'unknown'::text NOT NULL,
    last_heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT device_presence_addressing_check CHECK ((jsonb_typeof(addressing) = 'object'::text)),
    CONSTRAINT device_presence_connection_hint_check CHECK ((connection_hint = ANY (ARRAY['unknown'::text, 'direct'::text, 'relay'::text]))),
    CONSTRAINT device_presence_p2p_endpoint_id_check CHECK ((p2p_endpoint_id ~ '^[A-Za-z0-9_-]{32,128}$'::text)),
    CONSTRAINT device_presence_protocol_version_check CHECK ((protocol_version = 'misty-device/1'::text))
);

ALTER TABLE ONLY public.device_presence FORCE ROW LEVEL SECURITY;

CREATE TABLE public.figma_comment_audit (
    id text NOT NULL,
    space_id text NOT NULL,
    binding_id text NOT NULL,
    actor_user_id text,
    source text NOT NULL,
    idempotency_key text DEFAULT ''::text NOT NULL,
    action_fingerprint text DEFAULT ''::text NOT NULL,
    file_key text NOT NULL,
    target_node_id text DEFAULT ''::text NOT NULL,
    confirmed boolean NOT NULL,
    success boolean NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT figma_comment_audit_source_check CHECK ((source = ANY (ARRAY['user'::text, 'agent'::text])))
);

ALTER TABLE ONLY public.figma_comment_audit FORCE ROW LEVEL SECURITY;

CREATE TABLE public.figma_content_records (
    id text NOT NULL,
    space_id text NOT NULL,
    binding_id text NOT NULL,
    file_key text NOT NULL,
    record_type text NOT NULL,
    external_id text NOT NULL,
    parent_external_id text DEFAULT ''::text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    actor_id text DEFAULT ''::text NOT NULL,
    actor_name text DEFAULT ''::text NOT NULL,
    resolved boolean,
    fingerprint text NOT NULL,
    provenance jsonb DEFAULT '{}'::jsonb NOT NULL,
    occurred_at timestamp with time zone,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT figma_content_records_fingerprint_check CHECK ((fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT figma_content_records_provenance_check CHECK ((jsonb_typeof(provenance) = 'object'::text)),
    CONSTRAINT figma_content_records_record_type_check CHECK ((record_type = ANY (ARRAY['file'::text, 'version'::text, 'comment'::text, 'webhook_event'::text])))
);

ALTER TABLE ONLY public.figma_content_records FORCE ROW LEVEL SECURITY;

CREATE TABLE public.figma_space_bindings (
    id text NOT NULL,
    space_id text NOT NULL,
    connection_id text NOT NULL,
    integration_id text NOT NULL,
    shared_resource_id text NOT NULL,
    bound_by_user_id text NOT NULL,
    resource_type text NOT NULL,
    external_id text NOT NULL,
    display_name text NOT NULL,
    team_id text DEFAULT ''::text NOT NULL,
    project_id text DEFAULT ''::text NOT NULL,
    file_key text DEFAULT ''::text NOT NULL,
    sync_cursor text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    last_synced_at timestamp with time zone,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT figma_space_bindings_check CHECK ((((resource_type = 'file'::text) AND (file_key = external_id) AND (project_id = ''::text)) OR ((resource_type = 'project'::text) AND (project_id = external_id) AND (file_key = ''::text)))),
    CONSTRAINT figma_space_bindings_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 240))),
    CONSTRAINT figma_space_bindings_resource_type_check CHECK ((resource_type = ANY (ARRAY['file'::text, 'project'::text]))),
    CONSTRAINT figma_space_bindings_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.figma_space_bindings FORCE ROW LEVEL SECURITY;

CREATE TABLE public.figma_webhook_deliveries (
    delivery_hash text NOT NULL,
    subscription_id text NOT NULL,
    webhook_id text NOT NULL,
    event_type text NOT NULL,
    file_key text DEFAULT ''::text NOT NULL,
    event_timestamp timestamp with time zone,
    state text DEFAULT 'processing'::text NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    CONSTRAINT figma_webhook_deliveries_delivery_hash_check CHECK ((delivery_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT figma_webhook_deliveries_state_check CHECK ((state = ANY (ARRAY['processing'::text, 'processed'::text, 'ignored'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.figma_webhook_deliveries FORCE ROW LEVEL SECURITY;

CREATE TABLE public.figma_webhook_subscriptions (
    id text NOT NULL,
    binding_id text NOT NULL,
    webhook_id text NOT NULL,
    event_type text NOT NULL,
    passcode_hash text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT figma_webhook_subscriptions_event_type_check CHECK ((event_type = ANY (ARRAY['FILE_UPDATE'::text, 'FILE_VERSION_UPDATE'::text, 'FILE_COMMENT'::text]))),
    CONSTRAINT figma_webhook_subscriptions_passcode_hash_check CHECK ((passcode_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT figma_webhook_subscriptions_status_check CHECK ((status = ANY (ARRAY['active'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.figma_webhook_subscriptions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_app_installations (
    id text NOT NULL,
    space_id text NOT NULL,
    integration_id text NOT NULL,
    installed_by_user_id text NOT NULL,
    installation_id bigint NOT NULL,
    account_id bigint NOT NULL,
    account_login text NOT NULL,
    account_type text NOT NULL,
    repository_selection text DEFAULT 'selected'::text NOT NULL,
    permissions jsonb DEFAULT '{}'::jsonb NOT NULL,
    events jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    suspended_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT github_app_installations_account_type_check CHECK ((account_type = ANY (ARRAY['User'::text, 'Organization'::text, 'Enterprise'::text, 'Bot'::text]))),
    CONSTRAINT github_app_installations_events_check CHECK ((jsonb_typeof(events) = 'array'::text)),
    CONSTRAINT github_app_installations_permissions_check CHECK ((jsonb_typeof(permissions) = 'object'::text)),
    CONSTRAINT github_app_installations_repository_selection_check CHECK ((repository_selection = ANY (ARRAY['all'::text, 'selected'::text]))),
    CONSTRAINT github_app_installations_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.github_app_installations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_app_setup_states (
    state_hash text NOT NULL,
    user_id text NOT NULL,
    space_id text NOT NULL,
    return_to text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT github_app_setup_states_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.github_app_setup_states FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_code_workspaces (
    id text NOT NULL,
    space_id text NOT NULL,
    installation_id text NOT NULL,
    shared_resource_id text NOT NULL,
    bound_by_user_id text NOT NULL,
    repository_id bigint NOT NULL,
    full_name text NOT NULL,
    default_branch text DEFAULT ''::text NOT NULL,
    clone_url text NOT NULL,
    html_url text NOT NULL,
    private boolean DEFAULT false NOT NULL,
    client_workspace_id text DEFAULT ''::text NOT NULL,
    permissions jsonb DEFAULT '{}'::jsonb NOT NULL,
    sync_cursor text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    last_synced_at timestamp with time zone,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT github_code_workspaces_client_workspace_id_check CHECK (((client_workspace_id = ''::text) OR ((char_length(client_workspace_id) >= 8) AND (char_length(client_workspace_id) <= 200)))),
    CONSTRAINT github_code_workspaces_permissions_check CHECK ((jsonb_typeof(permissions) = 'object'::text)),
    CONSTRAINT github_code_workspaces_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.github_code_workspaces FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_credential_handoffs (
    handle_hash text NOT NULL,
    user_id text NOT NULL,
    space_id text NOT NULL,
    workspace_id text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT github_credential_handoffs_handle_hash_check CHECK ((handle_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.github_credential_handoffs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_mutation_audit (
    id text NOT NULL,
    space_id text NOT NULL,
    workspace_id text NOT NULL,
    actor_user_id text,
    source text NOT NULL,
    operation text NOT NULL,
    confirmed boolean NOT NULL,
    success boolean NOT NULL,
    target_ref text DEFAULT ''::text NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT github_mutation_audit_operation_check CHECK ((operation = ANY (ARRAY['create_issue'::text, 'comment_issue'::text, 'create_branch'::text, 'create_pull_request'::text]))),
    CONSTRAINT github_mutation_audit_source_check CHECK ((source = ANY (ARRAY['user'::text, 'agent'::text])))
);

ALTER TABLE ONLY public.github_mutation_audit FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_repository_records (
    id text NOT NULL,
    space_id text NOT NULL,
    workspace_id text NOT NULL,
    repository_id bigint NOT NULL,
    record_type text NOT NULL,
    external_id text NOT NULL,
    parent_external_id text DEFAULT ''::text NOT NULL,
    ref_name text DEFAULT ''::text NOT NULL,
    sha text DEFAULT ''::text NOT NULL,
    number bigint,
    state text DEFAULT ''::text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    actor_login text DEFAULT ''::text NOT NULL,
    fingerprint text NOT NULL,
    provenance jsonb DEFAULT '{}'::jsonb NOT NULL,
    occurred_at timestamp with time zone,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT github_repository_records_provenance_check CHECK ((jsonb_typeof(provenance) = 'object'::text)),
    CONSTRAINT github_repository_records_record_type_check CHECK ((record_type = ANY (ARRAY['repository'::text, 'branch'::text, 'commit'::text, 'issue'::text, 'pull_request'::text])))
);

ALTER TABLE ONLY public.github_repository_records FORCE ROW LEVEL SECURITY;

CREATE TABLE public.github_webhook_deliveries (
    delivery_id text NOT NULL,
    event_name text NOT NULL,
    action text DEFAULT ''::text NOT NULL,
    installation_id bigint,
    repository_id bigint,
    payload_sha256 text NOT NULL,
    state text DEFAULT 'processing'::text NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    CONSTRAINT github_webhook_deliveries_payload_sha256_check CHECK ((payload_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT github_webhook_deliveries_state_check CHECK ((state = ANY (ARRAY['processing'::text, 'processed'::text, 'ignored'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.github_webhook_deliveries FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_blobs (
    id text NOT NULL,
    security_domain_id text NOT NULL,
    r2_object_key text NOT NULL,
    sha256 text NOT NULL,
    byte_size bigint NOT NULL,
    client_declared_mime_type text DEFAULT ''::text NOT NULL,
    server_detected_mime_type text DEFAULT ''::text NOT NULL,
    scan_status text DEFAULT 'pending'::text NOT NULL,
    processing_status text DEFAULT 'pending'::text NOT NULL,
    lifecycle_state text DEFAULT 'quarantined'::text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT library_blobs_byte_size_check CHECK ((byte_size > 0)),
    CONSTRAINT library_blobs_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['quarantined'::text, 'ready'::text, 'trash'::text, 'purging'::text, 'deleted'::text, 'rejected'::text, 'infected'::text, 'invalid'::text]))),
    CONSTRAINT library_blobs_processing_status_check CHECK ((processing_status = ANY (ARRAY['pending'::text, 'processing'::text, 'ready'::text, 'failed'::text]))),
    CONSTRAINT library_blobs_scan_status_check CHECK ((scan_status = ANY (ARRAY['pending'::text, 'clean'::text, 'infected'::text, 'failed'::text, 'skipped'::text]))),
    CONSTRAINT library_blobs_sha256_check CHECK ((sha256 ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.library_blobs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_derivatives (
    id text NOT NULL,
    security_domain_id text NOT NULL,
    source_file_id text NOT NULL,
    space_library_item_id text,
    derivative_blob_id text,
    kind text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    lifecycle_state text DEFAULT 'processing'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_derivatives_kind_check CHECK ((kind = ANY (ARRAY['thumbnail'::text, 'image_preview'::text, 'document_preview'::text, 'video_transcode'::text, 'audio_waveform'::text, 'ocr'::text, 'ai_metadata'::text, 'embedding'::text, 'face_embedding'::text, 'search_document'::text, 'export'::text, 'duplicate_fingerprint'::text]))),
    CONSTRAINT library_derivatives_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['processing'::text, 'ready'::text, 'failed'::text, 'recovery'::text, 'purging'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.library_derivatives FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_exports (
    id text NOT NULL,
    security_domain_id text NOT NULL,
    space_id text NOT NULL,
    requested_by_user_id text NOT NULL,
    selection jsonb NOT NULL,
    export_blob_id text,
    state text DEFAULT 'queued'::text NOT NULL,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_exports_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'ready'::text, 'failed'::text, 'expired'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.library_exports FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_files (
    id text NOT NULL,
    blob_id text NOT NULL,
    security_domain_id text NOT NULL,
    uploader_user_id text NOT NULL,
    original_filename text NOT NULL,
    intrinsic_metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    lifecycle_state text DEFAULT 'quarantined'::text NOT NULL,
    original_uploaded_at timestamp with time zone DEFAULT now() NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    intrinsic_capture_at timestamp with time zone,
    intrinsic_location jsonb,
    CONSTRAINT library_files_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['quarantined'::text, 'ready'::text, 'trash'::text, 'purging'::text, 'deleted'::text, 'rejected'::text, 'infected'::text, 'invalid'::text]))),
    CONSTRAINT library_files_original_filename_check CHECK (((char_length(original_filename) >= 1) AND (char_length(original_filename) <= 255)))
);

ALTER TABLE ONLY public.library_files FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_item_versions (
    id text NOT NULL,
    space_library_item_id text NOT NULL,
    parent_version_id text,
    created_by_user_id text NOT NULL,
    rendition_blob_id text,
    edit_definition jsonb DEFAULT '{}'::jsonb NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    version_number bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    rendition_state text DEFAULT 'none'::text NOT NULL,
    rendition_mime_type text DEFAULT ''::text NOT NULL,
    rendition_byte_size bigint,
    rendition_error_code text DEFAULT ''::text NOT NULL,
    rendition_updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_item_versions_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'recovery'::text, 'purging'::text, 'deleted'::text]))),
    CONSTRAINT library_item_versions_rendition_byte_size_check CHECK (((rendition_byte_size IS NULL) OR (rendition_byte_size > 0))),
    CONSTRAINT library_item_versions_rendition_state_check CHECK ((rendition_state = ANY (ARRAY['none'::text, 'queued'::text, 'processing'::text, 'ready'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.library_item_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_legal_holds (
    id text NOT NULL,
    security_domain_id text NOT NULL,
    space_id text,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    reason_code text NOT NULL,
    created_by_user_id text,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    released_at timestamp with time zone
);

ALTER TABLE ONLY public.library_legal_holds FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_processing_jobs (
    id text NOT NULL,
    security_domain_id text NOT NULL,
    space_id text,
    job_kind text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    priority smallint DEFAULT 0 NOT NULL,
    state text DEFAULT 'queued'::text NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    lease_token text,
    lease_owner text,
    lease_expires_at timestamp with time zone,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    billing_user_id text,
    CONSTRAINT library_processing_jobs_job_kind_check CHECK ((job_kind = ANY (ARRAY['verify'::text, 'scan'::text, 'preview'::text, 'ocr'::text, 'metadata'::text, 'ai'::text, 'faces'::text, 'media'::text, 'edit'::text, 'duplicates'::text, 'export'::text, 'retention'::text, 'gc'::text, 'quota_reconcile'::text, 'r2_reconcile'::text, 'import'::text]))),
    CONSTRAINT library_processing_jobs_max_attempts_check CHECK (((max_attempts >= 1) AND (max_attempts <= 20))),
    CONSTRAINT library_processing_jobs_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'leased'::text, 'running'::text, 'completed'::text, 'failed'::text, 'dead'::text, 'canceled'::text])))
);

ALTER TABLE ONLY public.library_processing_jobs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_reauthentication_grants (
    id text NOT NULL,
    user_id text NOT NULL,
    space_id text NOT NULL,
    scope text NOT NULL,
    token_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_reauthentication_grants_scope_check CHECK ((scope = ANY (ARRAY['hidden'::text, 'recently_deleted'::text, 'bulk_export'::text])))
);

ALTER TABLE ONLY public.library_reauthentication_grants FORCE ROW LEVEL SECURITY;

CREATE TABLE public.library_recovery_tombstones (
    id text NOT NULL,
    security_domain_id text NOT NULL,
    space_id text,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    lifecycle_state text DEFAULT 'recovery'::text NOT NULL,
    recover_until timestamp with time zone NOT NULL,
    delete_lease_token text,
    delete_lease_expires_at timestamp with time zone,
    target_version bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_recovery_tombstones_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['recovery'::text, 'purging'::text, 'purged'::text, 'restored'::text, 'held'::text]))),
    CONSTRAINT library_recovery_tombstones_target_kind_check CHECK ((target_kind = ANY (ARRAY['space_item'::text, 'attachment'::text, 'edit'::text, 'file'::text, 'blob'::text, 'export'::text, 'import'::text])))
);

ALTER TABLE ONLY public.library_recovery_tombstones FORCE ROW LEVEL SECURITY;

CREATE TABLE public.licenses (
    user_id text NOT NULL,
    tier text DEFAULT 'free'::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    license_device text DEFAULT ''::text NOT NULL,
    id text NOT NULL,
    trial_started_at timestamp with time zone,
    legacy_tier text
);

ALTER TABLE ONLY public.licenses FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mail_action_audit (
    id bigint NOT NULL,
    user_id text NOT NULL,
    connection_id text NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    source text NOT NULL,
    confirmed boolean DEFAULT false NOT NULL,
    success boolean NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone DEFAULT now(),
    CONSTRAINT mail_action_audit_action_check CHECK ((action = ANY (ARRAY['thread_modify'::text, 'draft_create'::text, 'draft_update'::text, 'draft_send'::text]))),
    CONSTRAINT mail_action_audit_error_code_check CHECK ((char_length(error_code) <= 120)),
    CONSTRAINT mail_action_audit_source_check CHECK ((source = ANY (ARRAY['user'::text, 'ai'::text]))),
    CONSTRAINT mail_action_audit_target_id_check CHECK (((char_length(target_id) >= 1) AND (char_length(target_id) <= 320))),
    CONSTRAINT mail_action_audit_target_type_check CHECK ((target_type = ANY (ARRAY['thread'::text, 'draft'::text]))),
    CONSTRAINT mail_action_completion_valid CHECK (((completed_at IS NOT NULL) OR ((NOT success) AND (error_code = 'mail_operation_pending'::text))))
);

ALTER TABLE ONLY public.mail_action_audit FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.mail_action_audit_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.mail_action_audit_id_seq OWNED BY public.mail_action_audit.id;

CREATE TABLE public.mcp_discovery_snapshots (
    id text NOT NULL,
    connection_id text NOT NULL,
    protocol_version text DEFAULT ''::text NOT NULL,
    server_name text DEFAULT ''::text NOT NULL,
    server_version text DEFAULT ''::text NOT NULL,
    catalog_fingerprint text NOT NULL,
    tool_count integer NOT NULL,
    status text NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    discovered_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT mcp_discovery_snapshots_catalog_fingerprint_check CHECK ((catalog_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT mcp_discovery_snapshots_status_check CHECK ((status = ANY (ARRAY['complete'::text, 'rejected'::text]))),
    CONSTRAINT mcp_discovery_snapshots_tool_count_check CHECK ((tool_count >= 0))
);

ALTER TABLE ONLY public.mcp_discovery_snapshots FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mcp_oauth_credentials (
    connection_id text NOT NULL,
    owner_user_id text NOT NULL,
    credential_ciphertext bytea NOT NULL,
    credential_nonce bytea NOT NULL,
    key_version smallint DEFAULT 1 NOT NULL,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT mcp_oauth_credentials_key_version_check CHECK ((key_version > 0))
);

ALTER TABLE ONLY public.mcp_oauth_credentials FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mcp_oauth_states (
    state_hash text NOT NULL,
    owner_user_id text NOT NULL,
    secret_ciphertext bytea NOT NULL,
    secret_nonce bytea NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT mcp_oauth_states_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.mcp_oauth_states FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mcp_remote_connections (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    name text NOT NULL,
    endpoint_url text NOT NULL,
    transport text DEFAULT 'streamable_http'::text NOT NULL,
    bearer_ciphertext bytea DEFAULT '\x'::bytea NOT NULL,
    bearer_nonce bytea DEFAULT '\x'::bytea NOT NULL,
    key_version integer DEFAULT 1 NOT NULL,
    status text DEFAULT 'unchecked'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    last_checked_at timestamp with time zone,
    last_discovered_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    provider text DEFAULT 'custom'::text NOT NULL,
    CONSTRAINT mcp_remote_connections_endpoint_url_check CHECK (((char_length(endpoint_url) >= 1) AND (char_length(endpoint_url) <= 2048))),
    CONSTRAINT mcp_remote_connections_key_version_check CHECK ((key_version > 0)),
    CONSTRAINT mcp_remote_connections_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 120))),
    CONSTRAINT mcp_remote_connections_provider_check CHECK ((provider = ANY (ARRAY['custom'::text, 'activepieces'::text]))),
    CONSTRAINT mcp_remote_connections_status_check CHECK ((status = ANY (ARRAY['unchecked'::text, 'active'::text, 'needs_attention'::text, 'revoked'::text]))),
    CONSTRAINT mcp_remote_connections_transport_check CHECK ((transport = 'streamable_http'::text))
);

ALTER TABLE ONLY public.mcp_remote_connections FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mcp_remote_tools (
    id text NOT NULL,
    connection_id text NOT NULL,
    remote_name text NOT NULL,
    stable_name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    input_schema jsonb DEFAULT '{}'::jsonb NOT NULL,
    schema_fingerprint text NOT NULL,
    schema_status text NOT NULL,
    disabled_reason text DEFAULT ''::text NOT NULL,
    discovered_at timestamp with time zone DEFAULT now() NOT NULL,
    removed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT mcp_remote_tools_description_check CHECK ((char_length(description) <= 4000)),
    CONSTRAINT mcp_remote_tools_input_schema_check CHECK ((jsonb_typeof(input_schema) = 'object'::text)),
    CONSTRAINT mcp_remote_tools_remote_name_check CHECK (((char_length(remote_name) >= 1) AND (char_length(remote_name) <= 240))),
    CONSTRAINT mcp_remote_tools_schema_fingerprint_check CHECK ((schema_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT mcp_remote_tools_schema_status_check CHECK ((schema_status = ANY (ARRAY['valid'::text, 'unsupported'::text]))),
    CONSTRAINT mcp_remote_tools_stable_name_check CHECK (((char_length(stable_name) >= 1) AND (char_length(stable_name) <= 120)))
);

ALTER TABLE ONLY public.mcp_remote_tools FORCE ROW LEVEL SECURITY;

CREATE TABLE public.mcp_tool_execution_audit (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    agent_id text NOT NULL,
    connection_id text NOT NULL,
    remote_tool_id text,
    remote_name text NOT NULL,
    stable_name text NOT NULL,
    run_id text,
    idempotency_key text NOT NULL,
    source text NOT NULL,
    approved boolean NOT NULL,
    success boolean NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    duration_ms integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT mcp_tool_execution_audit_duration_ms_check CHECK ((duration_ms >= 0))
);

ALTER TABLE ONLY public.mcp_tool_execution_audit FORCE ROW LEVEL SECURITY;

CREATE TABLE public.media_search_assets (
    user_id text NOT NULL,
    asset_id text NOT NULL,
    fingerprint text NOT NULL,
    media_type text NOT NULL,
    mime_type text NOT NULL,
    duration_ms bigint NOT NULL,
    status text DEFAULT 'processing'::text NOT NULL,
    indexed_through_ms bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    device_id text DEFAULT 'device_00000000000000000000000000000000'::text NOT NULL,
    CONSTRAINT media_search_assets_device_id_check CHECK ((device_id ~ '^device_[0-9a-f]{32}$'::text)),
    CONSTRAINT media_search_assets_duration_ms_check CHECK (((duration_ms > 0) AND (duration_ms <= 7200000))),
    CONSTRAINT media_search_assets_fingerprint_check CHECK ((length(fingerprint) = 64)),
    CONSTRAINT media_search_assets_indexed_through_ms_check CHECK ((indexed_through_ms >= 0)),
    CONSTRAINT media_search_assets_media_type_check CHECK ((media_type = ANY (ARRAY['audio'::text, 'video'::text]))),
    CONSTRAINT media_search_assets_status_check CHECK ((status = ANY (ARRAY['processing'::text, 'indexed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.media_search_assets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.media_search_chunks (
    user_id text NOT NULL,
    asset_id text NOT NULL,
    chunk_index integer NOT NULL,
    fingerprint text NOT NULL,
    start_ms bigint NOT NULL,
    end_ms bigint NOT NULL,
    status text DEFAULT 'processing'::text NOT NULL,
    failure_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    device_id text DEFAULT 'device_00000000000000000000000000000000'::text NOT NULL,
    CONSTRAINT media_search_chunks_check CHECK (((end_ms > start_ms) AND (end_ms <= 7200000))),
    CONSTRAINT media_search_chunks_chunk_index_check CHECK (((chunk_index >= 0) AND (chunk_index < 240))),
    CONSTRAINT media_search_chunks_device_id_check CHECK ((device_id ~ '^device_[0-9a-f]{32}$'::text)),
    CONSTRAINT media_search_chunks_fingerprint_check CHECK ((length(fingerprint) = 64)),
    CONSTRAINT media_search_chunks_start_ms_check CHECK ((start_ms >= 0)),
    CONSTRAINT media_search_chunks_status_check CHECK ((status = ANY (ARRAY['processing'::text, 'indexed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.media_search_chunks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.media_search_devices (
    user_id text NOT NULL,
    device_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT media_search_devices_device_id_check CHECK ((device_id ~ '^device_[0-9a-f]{32}$'::text))
);

ALTER TABLE ONLY public.media_search_devices FORCE ROW LEVEL SECURITY;

CREATE TABLE public.media_search_segments (
    id text NOT NULL,
    user_id text NOT NULL,
    asset_id text NOT NULL,
    chunk_index integer NOT NULL,
    start_ms bigint NOT NULL,
    end_ms bigint NOT NULL,
    segment_kind text NOT NULL,
    content text NOT NULL,
    transcript text DEFAULT ''::text NOT NULL,
    visual_description text DEFAULT ''::text NOT NULL,
    visible_text jsonb DEFAULT '[]'::jsonb NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    embedding public.vector(768),
    embedding_model text,
    search_tsv tsvector GENERATED ALWAYS AS (((setweight(to_tsvector('simple'::regconfig, COALESCE(content, ''::text)), 'A'::"char") || setweight(to_tsvector('simple'::regconfig, COALESCE((visible_text)::text, ''::text)), 'A'::"char")) || setweight(to_tsvector('simple'::regconfig, COALESCE((metadata)::text, ''::text)), 'B'::"char"))) STORED,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    device_id text DEFAULT 'device_00000000000000000000000000000000'::text NOT NULL,
    CONSTRAINT media_search_segments_check CHECK (((end_ms > start_ms) AND (end_ms <= 7200000))),
    CONSTRAINT media_search_segments_content_check CHECK ((length(content) <= 12000)),
    CONSTRAINT media_search_segments_device_id_check CHECK ((device_id ~ '^device_[0-9a-f]{32}$'::text)),
    CONSTRAINT media_search_segments_segment_kind_check CHECK ((segment_kind = ANY (ARRAY['spoken'::text, 'visual'::text]))),
    CONSTRAINT media_search_segments_start_ms_check CHECK ((start_ms >= 0))
);

ALTER TABLE ONLY public.media_search_segments FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_agent_execution_leases (
    owner_user_id text NOT NULL,
    agent_id text NOT NULL,
    space_id text,
    task_id text NOT NULL,
    window_label text NOT NULL,
    expires_at timestamp with time zone NOT NULL
);

ALTER TABLE ONLY public.misty_agent_execution_leases FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_ask_conversations (
    id text NOT NULL,
    user_id text NOT NULL,
    state jsonb DEFAULT '{}'::jsonb NOT NULL,
    active_until timestamp with time zone DEFAULT (now() + '02:00:00'::interval) NOT NULL,
    retention_expires_at timestamp with time zone DEFAULT (now() + '30 days'::interval) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    title text DEFAULT ''::text NOT NULL,
    space_id text,
    model_id text DEFAULT 'openai/gpt-6-astra'::text NOT NULL,
    model_catalog_version text DEFAULT ''::text NOT NULL,
    conversation_kind text DEFAULT 'misty'::text NOT NULL,
    origin_surface text DEFAULT ''::text NOT NULL,
    origin_href text DEFAULT ''::text NOT NULL,
    privacy_boundary text DEFAULT ''::text NOT NULL,
    reasoning_effort text DEFAULT 'high'::text NOT NULL,
    agent_id text,
    CONSTRAINT agent_conversations_conversation_kind_check CHECK ((conversation_kind = ANY (ARRAY['misty'::text, 'companion_task'::text]))),
    CONSTRAINT agent_conversations_id_check CHECK ((id ~ '^conversation_[0-9a-f-]{36}$'::text)),
    CONSTRAINT agent_conversations_state_check CHECK ((jsonb_typeof(state) = 'object'::text)),
    CONSTRAINT misty_conversations_managed_reasoning_check CHECK ((reasoning_effort = ANY (ARRAY[''::text, 'low'::text, 'medium'::text, 'high'::text, 'xhigh'::text])))
);

ALTER TABLE ONLY public.misty_ask_conversations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_ask_identities (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    icon text DEFAULT ''::text NOT NULL,
    instructions text DEFAULT ''::text NOT NULL,
    model_mode text DEFAULT 'automatic'::text NOT NULL,
    model_id text DEFAULT 'openai/gpt-6-astra'::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    reasoning_effort text DEFAULT 'high'::text NOT NULL,
    role text DEFAULT ''::text NOT NULL,
    avatar jsonb DEFAULT '{"kind": "preset", "accent": "indigo", "preset_id": "bot"}'::jsonb NOT NULL,
    default_run_mode text DEFAULT 'auto'::text NOT NULL,
    voice_id text DEFAULT 'alloy'::text NOT NULL,
    system_managed boolean DEFAULT false NOT NULL,
    CONSTRAINT misty_agent_model_mode_check CHECK ((model_mode = ANY (ARRAY['automatic'::text, 'pinned'::text]))),
    CONSTRAINT misty_agent_pinned_model_check CHECK (((model_mode = 'automatic'::text) OR (char_length(model_id) > 0))),
    CONSTRAINT personal_agents_avatar_check CHECK ((jsonb_typeof(avatar) = 'object'::text)),
    CONSTRAINT personal_agents_default_run_mode_check CHECK ((default_run_mode = ANY (ARRAY['ask'::text, 'auto'::text, 'full'::text]))),
    CONSTRAINT personal_agents_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80))),
    CONSTRAINT personal_agents_role_check CHECK ((char_length(role) <= 80))
);

ALTER TABLE ONLY public.misty_ask_identities FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_ask_identity_versions (
    id text NOT NULL,
    agent_id text NOT NULL,
    version bigint NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    icon text DEFAULT ''::text NOT NULL,
    instructions text DEFAULT ''::text NOT NULL,
    model_mode text NOT NULL,
    model_id text DEFAULT ''::text NOT NULL,
    reasoning_effort text DEFAULT ''::text NOT NULL,
    checksum_sha256 text NOT NULL,
    created_by_user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    role text DEFAULT ''::text NOT NULL,
    avatar jsonb DEFAULT '{"kind": "preset", "accent": "indigo", "preset_id": "bot"}'::jsonb NOT NULL,
    default_run_mode text DEFAULT 'auto'::text NOT NULL,
    voice_id text DEFAULT 'alloy'::text NOT NULL,
    CONSTRAINT personal_agent_versions_avatar_check CHECK ((jsonb_typeof(avatar) = 'object'::text)),
    CONSTRAINT personal_agent_versions_checksum_sha256_check CHECK ((checksum_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT personal_agent_versions_default_run_mode_check CHECK ((default_run_mode = ANY (ARRAY['ask'::text, 'auto'::text, 'full'::text]))),
    CONSTRAINT personal_agent_versions_model_mode_check CHECK ((model_mode = ANY (ARRAY['automatic'::text, 'pinned'::text]))),
    CONSTRAINT personal_agent_versions_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80))),
    CONSTRAINT personal_agent_versions_role_check CHECK ((char_length(role) <= 80)),
    CONSTRAINT personal_agent_versions_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.misty_ask_identity_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_ask_mcp_tools (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    agent_id text NOT NULL,
    connection_id text NOT NULL,
    remote_tool_id text NOT NULL,
    stable_name text NOT NULL,
    schema_fingerprint text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT personal_agent_mcp_tools_schema_fingerprint_check CHECK ((schema_fingerprint ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.misty_ask_mcp_tools FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_conversation_focus (
    user_id text NOT NULL,
    conversation_id text NOT NULL,
    space_id text NOT NULL,
    entity_kind text NOT NULL,
    entity_id text NOT NULL,
    label text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    source_tool text DEFAULT ''::text NOT NULL,
    source_run_id text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT misty_conversation_focus_conversation_id_check CHECK (((char_length(btrim(conversation_id)) >= 1) AND (char_length(btrim(conversation_id)) <= 255))),
    CONSTRAINT misty_conversation_focus_entity_id_check CHECK (((char_length(btrim(entity_id)) >= 1) AND (char_length(btrim(entity_id)) <= 255))),
    CONSTRAINT misty_conversation_focus_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['task'::text, 'person'::text, 'note'::text, 'drawing'::text, 'calendar_event'::text, 'roadmap'::text, 'library_item'::text, 'message'::text]))),
    CONSTRAINT misty_conversation_focus_label_check CHECK ((char_length(label) <= 500)),
    CONSTRAINT misty_conversation_focus_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT misty_conversation_focus_source_run_id_check CHECK ((char_length(source_run_id) <= 255)),
    CONSTRAINT misty_conversation_focus_source_tool_check CHECK ((char_length(source_tool) <= 120))
);

ALTER TABLE ONLY public.misty_conversation_focus FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_conversation_pending_actions (
    user_id text NOT NULL,
    conversation_id text NOT NULL,
    space_id text NOT NULL,
    intent text NOT NULL,
    target_kind text DEFAULT ''::text NOT NULL,
    target_id text DEFAULT ''::text NOT NULL,
    target_label text DEFAULT ''::text NOT NULL,
    question text NOT NULL,
    original_prompt text DEFAULT ''::text NOT NULL,
    evidence jsonb DEFAULT '[]'::jsonb NOT NULL,
    candidate_intents jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT misty_conversation_pending_actions_candidate_intents_check CHECK ((jsonb_typeof(candidate_intents) = 'array'::text)),
    CONSTRAINT misty_conversation_pending_actions_conversation_id_check CHECK (((char_length(btrim(conversation_id)) >= 1) AND (char_length(btrim(conversation_id)) <= 255))),
    CONSTRAINT misty_conversation_pending_actions_evidence_check CHECK ((jsonb_typeof(evidence) = 'array'::text)),
    CONSTRAINT misty_conversation_pending_actions_intent_check CHECK (((char_length(btrim(intent)) >= 1) AND (char_length(btrim(intent)) <= 120))),
    CONSTRAINT misty_conversation_pending_actions_original_prompt_check CHECK ((char_length(original_prompt) <= 20000)),
    CONSTRAINT misty_conversation_pending_actions_question_check CHECK (((char_length(btrim(question)) >= 1) AND (char_length(btrim(question)) <= 1000))),
    CONSTRAINT misty_conversation_pending_actions_target_id_check CHECK ((char_length(target_id) <= 255)),
    CONSTRAINT misty_conversation_pending_actions_target_kind_check CHECK ((char_length(target_kind) <= 80)),
    CONSTRAINT misty_conversation_pending_actions_target_label_check CHECK ((char_length(target_label) <= 500))
);

ALTER TABLE ONLY public.misty_conversation_pending_actions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.misty_instance (
    singleton boolean DEFAULT true NOT NULL,
    server_id text NOT NULL,
    display_name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT misty_instance_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 120))),
    CONSTRAINT misty_instance_server_id_check CHECK ((server_id ~ '^server_[0-9a-f-]{36}$'::text)),
    CONSTRAINT misty_instance_singleton_check CHECK (singleton)
);

CREATE TABLE public.misty_memories (
    id text NOT NULL,
    user_id text NOT NULL,
    space_id text,
    scope_key text NOT NULL,
    memory_key text NOT NULL,
    kind text DEFAULT 'fact'::text NOT NULL,
    content text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    source_conversation_id text,
    source_invocation_id text,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    forgotten_at timestamp with time zone,
    agent_id text,
    CONSTRAINT misty_memories_content_check CHECK (((char_length(btrim(content)) >= 1) AND (char_length(btrim(content)) <= 1000))),
    CONSTRAINT misty_memories_kind_check CHECK ((kind = ANY (ARRAY['fact'::text, 'preference'::text, 'instruction'::text]))),
    CONSTRAINT misty_memories_memory_key_check CHECK ((char_length(memory_key) = 64)),
    CONSTRAINT misty_memories_reason_check CHECK ((char_length(reason) <= 500)),
    CONSTRAINT misty_memories_scope_key_check CHECK (((char_length(scope_key) >= 1) AND (char_length(scope_key) <= 255)))
);

ALTER TABLE ONLY public.misty_memories FORCE ROW LEVEL SECURITY;

CREATE TABLE public.native_task_effects (
    id bigint NOT NULL,
    space_id text NOT NULL,
    actor_user_id text NOT NULL,
    task_id text NOT NULL,
    event_kind text NOT NULL,
    task_version bigint NOT NULL,
    payload jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_id uuid,
    lease_until timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error_code text,
    completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT native_task_effects_event_kind_check CHECK ((event_kind = ANY (ARRAY['created'::text, 'updated'::text, 'moved'::text, 'archived'::text]))),
    CONSTRAINT native_task_effects_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'completed'::text, 'canceled'::text])))
);

ALTER TABLE ONLY public.native_task_effects FORCE ROW LEVEL SECURITY;

CREATE TABLE public.object_deletion_jobs (
    object_key text NOT NULL,
    not_before timestamp with time zone NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_id uuid,
    lease_expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    created_by_user_id text,
    CONSTRAINT object_deletion_jobs_attempts_check CHECK ((attempts >= 0))
);

ALTER TABLE ONLY public.object_deletion_jobs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.owner_storage_usage (
    owner_user_id text NOT NULL,
    used_bytes bigint DEFAULT 0 NOT NULL,
    reserved_bytes bigint DEFAULT 0 NOT NULL,
    over_quota_since timestamp with time zone,
    version bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT owner_storage_usage_reserved_bytes_check CHECK ((reserved_bytes >= 0)),
    CONSTRAINT owner_storage_usage_used_bytes_check CHECK ((used_bytes >= 0))
);

ALTER TABLE ONLY public.owner_storage_usage FORCE ROW LEVEL SECURITY;

CREATE TABLE public.password_recovery_jobs (
    id uuid NOT NULL,
    request_order bigint NOT NULL,
    email text NOT NULL,
    token_key_id text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    issued_user_id text,
    issued_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_owner uuid,
    lease_expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT password_recovery_jobs_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT password_recovery_jobs_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'processing'::text, 'sent'::text, 'superseded'::text, 'expired'::text])))
);

ALTER TABLE ONLY public.password_recovery_jobs FORCE ROW LEVEL SECURITY;

ALTER TABLE public.password_recovery_jobs ALTER COLUMN request_order ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.password_recovery_jobs_request_order_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.password_reset_tokens (
    user_id text NOT NULL,
    hashed_token character varying(64) NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT password_reset_tokens_hashed_token_check CHECK ((char_length((hashed_token)::text) = 64))
);

ALTER TABLE ONLY public.password_reset_tokens FORCE ROW LEVEL SECURITY;

CREATE TABLE public.personal_space_templates (
    id text NOT NULL,
    user_id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    apps jsonb NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT personal_space_templates_apps_check CHECK ((jsonb_typeof(apps) = 'array'::text)),
    CONSTRAINT personal_space_templates_description_check CHECK ((char_length(description) <= 1000)),
    CONSTRAINT personal_space_templates_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80)))
);

ALTER TABLE ONLY public.personal_space_templates FORCE ROW LEVEL SECURITY;

CREATE TABLE public.provider_content_records (
    id text NOT NULL,
    space_id text NOT NULL,
    shared_resource_id text NOT NULL,
    provider text NOT NULL,
    external_record_id text NOT NULL,
    parent_external_id text DEFAULT ''::text NOT NULL,
    record_type text NOT NULL,
    fingerprint text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    mime_type text DEFAULT 'application/json'::text NOT NULL,
    occurred_at timestamp with time zone,
    content jsonb DEFAULT '{}'::jsonb NOT NULL,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_content_records_provider_check CHECK ((provider ~ '^[a-z][a-z0-9_]{1,39}$'::text))
);

ALTER TABLE ONLY public.provider_content_records FORCE ROW LEVEL SECURITY;

CREATE TABLE public.provider_event_inbox (
    id bigint NOT NULL,
    integration_id text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    external_event_id text NOT NULL,
    payload jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    CONSTRAINT provider_event_inbox_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'claimed'::text, 'processed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.provider_event_inbox FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.provider_event_inbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.provider_event_inbox_id_seq OWNED BY public.provider_event_inbox.id;

CREATE TABLE public.provider_gateway_state (
    provider text NOT NULL,
    session_id text DEFAULT ''::text NOT NULL,
    resume_url text DEFAULT ''::text NOT NULL,
    sequence bigint DEFAULT 0 NOT NULL,
    last_heartbeat_at timestamp with time zone,
    last_event_at timestamp with time zone,
    status text DEFAULT 'disconnected'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_gateway_state_provider_check CHECK ((provider = 'discord'::text)),
    CONSTRAINT provider_gateway_state_status_check CHECK ((status = ANY (ARRAY['connected'::text, 'degraded'::text, 'disconnected'::text])))
);

ALTER TABLE ONLY public.provider_gateway_state FORCE ROW LEVEL SECURITY;

CREATE TABLE public.provider_oauth_states (
    state_hash text NOT NULL,
    user_id text NOT NULL,
    space_id text NOT NULL,
    provider text NOT NULL,
    verifier_ciphertext bytea NOT NULL,
    verifier_nonce bytea NOT NULL,
    return_to text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_oauth_states_state_hash_check CHECK ((state_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.provider_oauth_states FORCE ROW LEVEL SECURITY;

CREATE TABLE public.provider_shared_resources (
    id text NOT NULL,
    space_id text NOT NULL,
    integration_id text NOT NULL,
    published_by_user_id text NOT NULL,
    provider text NOT NULL,
    resource_type text NOT NULL,
    external_resource_id text NOT NULL,
    display_name text NOT NULL,
    permission_scope text NOT NULL,
    configuration jsonb DEFAULT '{}'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_shared_resources_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 240))),
    CONSTRAINT provider_shared_resources_provider_check CHECK ((provider ~ '^[a-z][a-z0-9_]{1,39}$'::text)),
    CONSTRAINT provider_shared_resources_resource_type_check CHECK ((resource_type ~ '^[a-z][a-z0-9_]{1,39}$'::text)),
    CONSTRAINT provider_shared_resources_status_check CHECK ((status = ANY (ARRAY['active'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.provider_shared_resources FORCE ROW LEVEL SECURITY;

CREATE TABLE public.provider_subscriptions (
    id text NOT NULL,
    integration_id text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    resource_key text NOT NULL,
    external_subscription_id text NOT NULL,
    cursor jsonb DEFAULT '{}'::jsonb NOT NULL,
    expires_at timestamp with time zone,
    status text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_subscriptions_status_check CHECK ((status = ANY (ARRAY['active'::text, 'renewing'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.provider_subscriptions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.realtime_tickets (
    token_hash text NOT NULL,
    user_id text NOT NULL,
    after_cursor bigint DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.realtime_tickets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_backend_connection_versions (
    user_id text NOT NULL,
    id uuid NOT NULL,
    revision integer NOT NULL,
    endpoint_url text NOT NULL,
    bearer_ciphertext bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sdk_backend_connection_versions_bearer_ciphertext_check CHECK ((octet_length(bearer_ciphertext) > 16)),
    CONSTRAINT sdk_backend_connection_versions_endpoint_url_check CHECK (((char_length(endpoint_url) >= 1) AND (char_length(endpoint_url) <= 2048))),
    CONSTRAINT sdk_backend_connection_versions_key_version_check CHECK ((key_version > 0)),
    CONSTRAINT sdk_backend_connection_versions_revision_check CHECK ((revision > 0))
);

ALTER TABLE ONLY public.sdk_backend_connection_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_backend_connections (
    user_id text NOT NULL,
    id uuid NOT NULL,
    app_id text NOT NULL,
    revision integer NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    CONSTRAINT sdk_backend_connections_revision_check CHECK ((revision > 0))
);

ALTER TABLE ONLY public.sdk_backend_connections FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_capability_contract_versions (
    user_id text NOT NULL,
    name text NOT NULL,
    version integer NOT NULL,
    definition jsonb NOT NULL,
    CONSTRAINT sdk_capability_contract_versions_definition_check CHECK ((jsonb_typeof(definition) = 'object'::text)),
    CONSTRAINT sdk_capability_contract_versions_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.sdk_capability_contract_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_capability_invocations (
    user_id text NOT NULL,
    request_id uuid NOT NULL,
    caller_app_id text DEFAULT ''::text NOT NULL,
    invocation_id text NOT NULL,
    effect_id uuid NOT NULL,
    request jsonb NOT NULL,
    target_id uuid NOT NULL,
    target_revision integer NOT NULL,
    adapter_version text DEFAULT 'sdk-backend-v1'::text NOT NULL,
    outcome_ciphertext bytea,
    outcome_status text DEFAULT ''::text NOT NULL,
    cancel_requested_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    observed_outcome_ciphertext bytea,
    CONSTRAINT sdk_capability_invocations_outcome_status_check CHECK ((outcome_status = ANY (ARRAY[''::text, 'success'::text, 'failure'::text, 'approval_required'::text, 'device_required'::text, 'user_intervention_required'::text, 'uncertain'::text]))),
    CONSTRAINT sdk_capability_invocations_request_check CHECK ((jsonb_typeof(request) = 'object'::text))
);

ALTER TABLE ONLY public.sdk_capability_invocations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_provider_registrations (
    user_id text NOT NULL,
    provider_id text NOT NULL,
    version integer NOT NULL,
    app_id text NOT NULL,
    app_version text NOT NULL,
    installed_at timestamp with time zone NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    reported_state text DEFAULT 'unavailable'::text NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    space_id text NOT NULL,
    CONSTRAINT sdk_provider_registrations_reported_state_check CHECK ((reported_state = ANY (ARRAY['available'::text, 'device_required'::text, 'authentication_required'::text, 'account_confirmation_required'::text, 'view_closed'::text, 'unavailable'::text, 'revoked'::text])))
);

ALTER TABLE ONLY public.sdk_provider_registrations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_provider_versions (
    user_id text NOT NULL,
    provider_id text NOT NULL,
    version integer NOT NULL,
    app_id text NOT NULL,
    definition jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sdk_provider_versions_definition_check CHECK ((jsonb_typeof(definition) = 'object'::text)),
    CONSTRAINT sdk_provider_versions_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.sdk_provider_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_target_versions (
    user_id text NOT NULL,
    id uuid NOT NULL,
    revision integer NOT NULL,
    provider_id text NOT NULL,
    provider_version integer NOT NULL,
    app_version text NOT NULL,
    installed_at timestamp with time zone NOT NULL,
    space_id text,
    target jsonb NOT NULL,
    capabilities jsonb NOT NULL,
    caller_apps jsonb NOT NULL,
    connection_id uuid,
    connection_revision integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sdk_target_route_binding CHECK (((((target -> 'binding'::text) ->> 'kind'::text) IS NOT NULL) AND (((((target -> 'binding'::text) ->> 'kind'::text) = 'backend'::text) AND (connection_id IS NOT NULL) AND (connection_revision IS NOT NULL)) OR ((((target -> 'binding'::text) ->> 'kind'::text) = 'browser'::text) AND (connection_id IS NULL) AND (connection_revision IS NULL)) OR ((((target -> 'binding'::text) ->> 'kind'::text) = 'resource'::text) AND (provider_id = 'planner/tasks'::text) AND (connection_id IS NULL) AND (connection_revision IS NULL))))),
    CONSTRAINT sdk_target_versions_caller_apps_check CHECK ((jsonb_typeof(caller_apps) = 'array'::text)),
    CONSTRAINT sdk_target_versions_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'array'::text)),
    CONSTRAINT sdk_target_versions_revision_check CHECK ((revision > 0))
);

ALTER TABLE ONLY public.sdk_target_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sdk_targets (
    user_id text NOT NULL,
    id uuid NOT NULL,
    revision integer NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    CONSTRAINT sdk_targets_revision_check CHECK ((revision > 0))
);

ALTER TABLE ONLY public.sdk_targets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.security_domains (
    id text NOT NULL,
    kind text NOT NULL,
    owner_user_id text,
    space_id text,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT security_domains_check CHECK ((((kind = 'personal'::text) AND (owner_user_id IS NOT NULL) AND (space_id IS NULL)) OR ((kind = 'space'::text) AND (owner_user_id IS NOT NULL) AND (space_id IS NOT NULL)) OR (kind = 'organization'::text))),
    CONSTRAINT security_domains_kind_check CHECK ((kind = ANY (ARRAY['personal'::text, 'space'::text, 'organization'::text]))),
    CONSTRAINT security_domains_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'suspended'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.security_domains FORCE ROW LEVEL SECURITY;

CREATE TABLE public.self_host_accounts (
    user_id text NOT NULL,
    entitlement_subject text NOT NULL,
    entitlement_expires_at timestamp with time zone NOT NULL,
    is_admin boolean DEFAULT false NOT NULL,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT self_host_accounts_entitlement_subject_check CHECK (((char_length(entitlement_subject) >= 16) AND (char_length(entitlement_subject) <= 160)))
);

ALTER TABLE ONLY public.self_host_accounts FORCE ROW LEVEL SECURITY;

CREATE TABLE public.self_host_bootstrap_tokens (
    token_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT self_host_bootstrap_tokens_check CHECK ((expires_at <= (created_at + '00:30:05'::interval))),
    CONSTRAINT self_host_bootstrap_tokens_token_hash_check CHECK ((token_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.self_host_bootstrap_tokens FORCE ROW LEVEL SECURITY;

CREATE TABLE public.self_host_collaboration_documents (
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    state bytea NOT NULL,
    checksum_sha256 text NOT NULL,
    acl_version bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT self_host_collaboration_documents_acl_version_check CHECK ((acl_version >= 0)),
    CONSTRAINT self_host_collaboration_documents_checksum_sha256_check CHECK ((checksum_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT self_host_collaboration_documents_resource_id_check CHECK (((char_length(resource_id) >= 1) AND (char_length(resource_id) <= 200))),
    CONSTRAINT self_host_collaboration_documents_resource_type_check CHECK ((resource_type = ANY (ARRAY['note'::text, 'drawing'::text]))),
    CONSTRAINT self_host_collaboration_documents_state_check CHECK ((octet_length(state) <= 8388608))
);

ALTER TABLE ONLY public.self_host_collaboration_documents FORCE ROW LEVEL SECURITY;

CREATE TABLE public.self_host_enrollment_invitations (
    id text NOT NULL,
    token_hash text NOT NULL,
    created_by text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_by text,
    consumed_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT self_host_enrollment_invitations_check CHECK ((expires_at <= (created_at + '7 days 00:00:05'::interval))),
    CONSTRAINT self_host_enrollment_invitations_id_check CHECK ((id ~ '^enrollment_[0-9a-f-]{36}$'::text)),
    CONSTRAINT self_host_enrollment_invitations_token_hash_check CHECK ((token_hash ~ '^[0-9a-f]{64}$'::text))
);

ALTER TABLE ONLY public.self_host_enrollment_invitations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.sessions (
    token_hash text NOT NULL,
    user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    refresh_hash text
);

ALTER TABLE ONLY public.sessions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.smart_library_assets (
    folder_id text NOT NULL,
    asset_id text NOT NULL,
    fingerprint text NOT NULL,
    extension text NOT NULL,
    size_bytes bigint NOT NULL,
    modified_bucket bigint DEFAULT 0 NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    description text,
    tags jsonb DEFAULT '[]'::jsonb NOT NULL,
    collections jsonb DEFAULT '[]'::jsonb NOT NULL,
    confidence double precision,
    failure_code text,
    model text,
    embedding jsonb,
    result_sequence bigint,
    analyzed_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    counted_success boolean DEFAULT false NOT NULL,
    sample_eligible boolean DEFAULT false NOT NULL,
    user_id text NOT NULL,
    asset_kind text DEFAULT 'image'::text NOT NULL,
    mime_type text DEFAULT 'application/octet-stream'::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    semantic_embedding public.vector(768),
    embedding_model text,
    embedding_version integer DEFAULT 0 NOT NULL,
    embedding_input_hash text,
    embedded_at timestamp with time zone,
    index_status text DEFAULT 'pending'::text NOT NULL,
    index_failure_code text,
    search_tsv tsvector GENERATED ALWAYS AS ((((setweight(to_tsvector('simple'::regconfig, COALESCE(description, ''::text)), 'A'::"char") || setweight(to_tsvector('simple'::regconfig, COALESCE((tags)::text, ''::text)), 'A'::"char")) || setweight(to_tsvector('simple'::regconfig, COALESCE((metadata)::text, ''::text)), 'A'::"char")) || setweight(to_tsvector('simple'::regconfig, COALESCE((collections)::text, ''::text)), 'B'::"char"))) STORED,
    index_claim_token text,
    index_claimed_at timestamp with time zone,
    CONSTRAINT smart_library_assets_asset_kind_check CHECK ((asset_kind = ANY (ARRAY['image'::text, 'document'::text, 'text'::text, 'audio'::text, 'archive'::text, 'binary'::text]))),
    CONSTRAINT smart_library_assets_index_status_check CHECK ((index_status = ANY (ARRAY['pending'::text, 'processing'::text, 'indexed'::text, 'failed'::text]))),
    CONSTRAINT smart_library_assets_size_bytes_check CHECK ((size_bytes >= 0)),
    CONSTRAINT smart_library_assets_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'analyzed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.smart_library_assets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.smart_library_batches (
    id text NOT NULL,
    folder_id text NOT NULL,
    kind text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    asset_ids jsonb NOT NULL,
    successful_images integer DEFAULT 0 NOT NULL,
    failed_images integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT smart_library_batches_kind_check CHECK ((kind = ANY (ARRAY['sample'::text, 'full'::text, 'rescan'::text, 'organization'::text]))),
    CONSTRAINT smart_library_batches_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'completed'::text, 'partially_failed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.smart_library_batches FORCE ROW LEVEL SECURITY;

CREATE TABLE public.smart_library_cost_events (
    id bigint NOT NULL,
    user_id text NOT NULL,
    folder_id text,
    asset_id text,
    batch_id text,
    model text NOT NULL,
    batch_size integer NOT NULL,
    input_tokens bigint DEFAULT 0 NOT NULL,
    output_tokens bigint DEFAULT 0 NOT NULL,
    provider_cost_microusd bigint DEFAULT 0 NOT NULL,
    fallback_reason text,
    success boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    event_kind text DEFAULT 'analysis'::text NOT NULL,
    CONSTRAINT smart_library_cost_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['analysis'::text, 'semantic_index'::text, 'semantic_query'::text, 'reindex'::text])))
);

ALTER TABLE ONLY public.smart_library_cost_events FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.smart_library_cost_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.smart_library_cost_events_id_seq OWNED BY public.smart_library_cost_events.id;

CREATE TABLE public.smart_library_folders (
    id text NOT NULL,
    user_id text NOT NULL,
    client_library_id text NOT NULL,
    source_kind text NOT NULL,
    state text DEFAULT 'preflight'::text NOT NULL,
    successful_images integer DEFAULT 0 NOT NULL,
    failed_images integer DEFAULT 0 NOT NULL,
    eligible_images integer DEFAULT 0 NOT NULL,
    included_images integer DEFAULT 0 NOT NULL,
    billable_images integer DEFAULT 0 NOT NULL,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT smart_library_folders_source_kind_check CHECK ((source_kind = ANY (ARRAY['local'::text, 'cloud'::text]))),
    CONSTRAINT smart_library_folders_successful_images_nonnegative CHECK ((successful_images >= 0))
);

ALTER TABLE ONLY public.smart_library_folders FORCE ROW LEVEL SECURITY;

CREATE TABLE public.smart_library_reindex_jobs (
    id text NOT NULL,
    user_id text NOT NULL,
    folder_id text,
    status text DEFAULT 'pending'::text NOT NULL,
    embedding_model text NOT NULL,
    embedding_version integer NOT NULL,
    asset_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    completed_asset_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    failed_asset_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    cursor text DEFAULT ''::text NOT NULL,
    requested_assets integer DEFAULT 0 NOT NULL,
    completed_assets integer DEFAULT 0 NOT NULL,
    failed_assets integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT smart_library_reindex_jobs_completed_assets_check CHECK ((completed_assets >= 0)),
    CONSTRAINT smart_library_reindex_jobs_embedding_version_check CHECK ((embedding_version > 0)),
    CONSTRAINT smart_library_reindex_jobs_failed_assets_check CHECK ((failed_assets >= 0)),
    CONSTRAINT smart_library_reindex_jobs_requested_assets_check CHECK ((requested_assets >= 0)),
    CONSTRAINT smart_library_reindex_jobs_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'completed'::text, 'partially_failed'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.smart_library_reindex_jobs FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.smart_library_result_sequence
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE public.social_automation_rules (
    id text NOT NULL,
    space_id text NOT NULL,
    binding_id text NOT NULL,
    conversation_id text,
    authority_id text NOT NULL,
    created_by_user_id text NOT NULL,
    name text NOT NULL,
    instructions text NOT NULL,
    tone text DEFAULT ''::text NOT NULL,
    confidence_threshold numeric(4,3) DEFAULT 0.800 NOT NULL,
    max_replies_per_hour integer DEFAULT 5 NOT NULL,
    max_replies_per_day integer DEFAULT 25 NOT NULL,
    cooldown_seconds integer DEFAULT 120 NOT NULL,
    max_unanswered_replies integer DEFAULT 2 NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    paused_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_automation_rules_confidence_threshold_check CHECK (((confidence_threshold >= (0)::numeric) AND (confidence_threshold <= (1)::numeric))),
    CONSTRAINT social_automation_rules_cooldown_seconds_check CHECK (((cooldown_seconds >= 30) AND (cooldown_seconds <= 86400))),
    CONSTRAINT social_automation_rules_id_check CHECK ((id ~ '^social_rule_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_automation_rules_instructions_check CHECK (((char_length(instructions) >= 1) AND (char_length(instructions) <= 10000))),
    CONSTRAINT social_automation_rules_max_replies_per_day_check CHECK (((max_replies_per_day >= 1) AND (max_replies_per_day <= 1000))),
    CONSTRAINT social_automation_rules_max_replies_per_hour_check CHECK (((max_replies_per_hour >= 1) AND (max_replies_per_hour <= 100))),
    CONSTRAINT social_automation_rules_max_unanswered_replies_check CHECK (((max_unanswered_replies >= 1) AND (max_unanswered_replies <= 10))),
    CONSTRAINT social_automation_rules_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 120))),
    CONSTRAINT social_automation_rules_tone_check CHECK ((char_length(tone) <= 500))
);

ALTER TABLE ONLY public.social_automation_rules FORCE ROW LEVEL SECURITY;

CREATE TABLE public.social_automation_runs (
    id text NOT NULL,
    space_id text NOT NULL,
    rule_id text NOT NULL,
    trigger_message_id text,
    outbound_command_id text,
    decision text NOT NULL,
    reason_code text DEFAULT ''::text NOT NULL,
    confidence numeric(4,3),
    draft_content jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_automation_runs_decision_check CHECK ((decision = ANY (ARRAY['reply'::text, 'draft'::text, 'skip'::text, 'blocked'::text]))),
    CONSTRAINT social_automation_runs_draft_content_check CHECK ((jsonb_typeof(draft_content) = 'array'::text)),
    CONSTRAINT social_automation_runs_id_check CHECK ((id ~ '^social_run_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_automation_runs_reason_code_check CHECK ((char_length(reason_code) <= 120))
);

ALTER TABLE ONLY public.social_automation_runs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.social_bindings (
    id text NOT NULL,
    space_id text NOT NULL,
    connection_id text NOT NULL,
    connected_by_user_id text NOT NULL,
    conversation_id text,
    provider text NOT NULL,
    external_resource_id text NOT NULL,
    external_parent_id text DEFAULT ''::text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    direction text DEFAULT 'two_way'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    capabilities jsonb DEFAULT '{}'::jsonb NOT NULL,
    sync_cursor text DEFAULT ''::text NOT NULL,
    last_synced_at timestamp with time zone,
    last_error_code text DEFAULT ''::text NOT NULL,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_bindings_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'object'::text)),
    CONSTRAINT social_bindings_direction_check CHECK ((direction = ANY (ARRAY['two_way'::text, 'inbound'::text, 'outbound'::text]))),
    CONSTRAINT social_bindings_display_name_check CHECK ((char_length(display_name) <= 320)),
    CONSTRAINT social_bindings_external_parent_id_check CHECK ((char_length(external_parent_id) <= 320)),
    CONSTRAINT social_bindings_external_resource_id_check CHECK (((char_length(external_resource_id) >= 1) AND (char_length(external_resource_id) <= 320))),
    CONSTRAINT social_bindings_id_check CHECK ((id ~ '^social_binding_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_bindings_last_error_code_check CHECK ((char_length(last_error_code) <= 120)),
    CONSTRAINT social_bindings_provider_check CHECK ((provider = ANY (ARRAY['discord'::text, 'instagram'::text]))),
    CONSTRAINT social_bindings_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'syncing'::text, 'active'::text, 'needs_attention'::text, 'disabled'::text]))),
    CONSTRAINT social_bindings_sync_cursor_check CHECK ((char_length(sync_cursor) <= 1000))
);

ALTER TABLE ONLY public.social_bindings FORCE ROW LEVEL SECURITY;

CREATE TABLE public.social_identities (
    id text NOT NULL,
    binding_id text NOT NULL,
    provider text NOT NULL,
    external_user_id text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    handle text DEFAULT ''::text NOT NULL,
    avatar_url text DEFAULT ''::text NOT NULL,
    kind text DEFAULT 'person'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_identities_avatar_url_check CHECK ((char_length(avatar_url) <= 2048)),
    CONSTRAINT social_identities_display_name_check CHECK ((char_length(display_name) <= 320)),
    CONSTRAINT social_identities_external_user_id_check CHECK (((char_length(external_user_id) >= 1) AND (char_length(external_user_id) <= 320))),
    CONSTRAINT social_identities_handle_check CHECK ((char_length(handle) <= 320)),
    CONSTRAINT social_identities_id_check CHECK ((id ~ '^social_identity_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_identities_kind_check CHECK ((kind = ANY (ARRAY['person'::text, 'business'::text, 'bot'::text]))),
    CONSTRAINT social_identities_provider_check CHECK ((provider = ANY (ARRAY['discord'::text, 'instagram'::text])))
);

ALTER TABLE ONLY public.social_identities FORCE ROW LEVEL SECURITY;

CREATE TABLE public.social_outbound_commands (
    id text NOT NULL,
    space_id text NOT NULL,
    binding_id text NOT NULL,
    conversation_id text NOT NULL,
    authority_id text,
    requested_by_user_id text,
    source_kind text NOT NULL,
    content jsonb NOT NULL,
    idempotency_key text NOT NULL,
    state text DEFAULT 'queued'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_expires_at timestamp with time zone,
    provider_receipt jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_outbound_commands_attempts_check CHECK (((attempts >= 0) AND (attempts <= 20))),
    CONSTRAINT social_outbound_commands_content_check CHECK ((jsonb_typeof(content) = 'array'::text)),
    CONSTRAINT social_outbound_commands_id_check CHECK ((id ~ '^social_command_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_outbound_commands_idempotency_key_check CHECK (((char_length(idempotency_key) >= 8) AND (char_length(idempotency_key) <= 200))),
    CONSTRAINT social_outbound_commands_last_error_code_check CHECK ((char_length(last_error_code) <= 120)),
    CONSTRAINT social_outbound_commands_provider_receipt_check CHECK ((jsonb_typeof(provider_receipt) = 'object'::text)),
    CONSTRAINT social_outbound_commands_source_kind_check CHECK ((source_kind = ANY (ARRAY['manual'::text, 'scheduled'::text, 'automation'::text]))),
    CONSTRAINT social_outbound_commands_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'sending'::text, 'sent'::text, 'failed'::text, 'cancelled'::text])))
);

ALTER TABLE ONLY public.social_outbound_commands FORCE ROW LEVEL SECURITY;

CREATE TABLE public.social_scheduled_messages (
    id text NOT NULL,
    space_id text NOT NULL,
    binding_id text NOT NULL,
    conversation_id text NOT NULL,
    authority_id text NOT NULL,
    created_by_user_id text NOT NULL,
    content jsonb NOT NULL,
    scheduled_at timestamp with time zone NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    status text DEFAULT 'scheduled'::text NOT NULL,
    outbound_command_id text,
    last_error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_scheduled_messages_content_check CHECK ((jsonb_typeof(content) = 'array'::text)),
    CONSTRAINT social_scheduled_messages_id_check CHECK ((id ~ '^social_scheduled_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_scheduled_messages_last_error_code_check CHECK ((char_length(last_error_code) <= 120)),
    CONSTRAINT social_scheduled_messages_status_check CHECK ((status = ANY (ARRAY['scheduled'::text, 'queued'::text, 'sent'::text, 'failed'::text, 'cancelled'::text]))),
    CONSTRAINT social_scheduled_messages_timezone_check CHECK ((char_length(timezone) <= 80))
);

ALTER TABLE ONLY public.social_scheduled_messages FORCE ROW LEVEL SECURITY;

CREATE TABLE public.social_send_authorities (
    id text NOT NULL,
    space_id text NOT NULL,
    user_id text NOT NULL,
    connection_id text NOT NULL,
    binding_id text,
    allow_manual boolean DEFAULT true NOT NULL,
    allow_scheduled boolean DEFAULT false NOT NULL,
    allow_automation boolean DEFAULT false NOT NULL,
    hourly_limit integer DEFAULT 5 NOT NULL,
    daily_limit integer DEFAULT 25 NOT NULL,
    quiet_hours jsonb DEFAULT '{}'::jsonb NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    approved_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT social_send_authorities_daily_limit_check CHECK (((daily_limit >= 1) AND (daily_limit <= 1000))),
    CONSTRAINT social_send_authorities_hourly_limit_check CHECK (((hourly_limit >= 1) AND (hourly_limit <= 100))),
    CONSTRAINT social_send_authorities_id_check CHECK ((id ~ '^social_authority_[0-9a-f-]{36}$'::text)),
    CONSTRAINT social_send_authorities_quiet_hours_check CHECK ((jsonb_typeof(quiet_hours) = 'object'::text)),
    CONSTRAINT social_send_authorities_timezone_check CHECK ((char_length(timezone) <= 80))
);

ALTER TABLE ONLY public.social_send_authorities FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_album_folders (
    id text NOT NULL,
    space_id text NOT NULL,
    parent_folder_id text,
    name text NOT NULL,
    "position" bigint DEFAULT 0 NOT NULL,
    created_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_album_folders_check CHECK (((parent_folder_id IS NULL) OR (parent_folder_id <> id))),
    CONSTRAINT space_album_folders_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 120)))
);

ALTER TABLE ONLY public.space_album_folders FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_album_items (
    album_id text NOT NULL,
    space_library_item_id text NOT NULL,
    added_by_user_id text NOT NULL,
    "position" bigint DEFAULT 0 NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_album_items FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_albums (
    id text NOT NULL,
    space_id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    cover_item_id text,
    created_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    folder_id text,
    "position" bigint DEFAULT 0 NOT NULL,
    view_mode text DEFAULT 'grid'::text NOT NULL,
    sort_mode text DEFAULT 'custom'::text NOT NULL,
    CONSTRAINT space_albums_description_check CHECK ((char_length(description) <= 2000)),
    CONSTRAINT space_albums_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 120))),
    CONSTRAINT space_albums_sort_mode_check CHECK ((sort_mode = ANY (ARRAY['custom'::text, 'oldest'::text, 'newest'::text]))),
    CONSTRAINT space_albums_view_mode_check CHECK ((view_mode = ANY (ARRAY['grid'::text, 'list'::text])))
);

ALTER TABLE ONLY public.space_albums FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_calendar_events (
    id text NOT NULL,
    space_id text NOT NULL,
    source_id text NOT NULL,
    provider text NOT NULL,
    external_event_id text NOT NULL,
    fingerprint text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    location text DEFAULT ''::text NOT NULL,
    meeting_url text DEFAULT ''::text NOT NULL,
    organizer jsonb DEFAULT '{}'::jsonb NOT NULL,
    starts_at timestamp with time zone NOT NULL,
    ends_at timestamp with time zone NOT NULL,
    all_day boolean DEFAULT false NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    status text DEFAULT 'confirmed'::text NOT NULL,
    provider_created_at timestamp with time zone,
    provider_updated_at timestamp with time zone,
    removed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_calendar_events_check CHECK ((ends_at >= starts_at)),
    CONSTRAINT space_calendar_events_provider_check CHECK ((provider = 'google'::text)),
    CONSTRAINT space_calendar_events_status_check CHECK ((status = ANY (ARRAY['confirmed'::text, 'tentative'::text, 'canceled'::text])))
);

ALTER TABLE ONLY public.space_calendar_events FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_calendar_sources (
    id text NOT NULL,
    space_id text NOT NULL,
    integration_id text NOT NULL,
    connected_by_user_id text NOT NULL,
    provider text NOT NULL,
    external_calendar_id text NOT NULL,
    display_name text NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    sync_token text DEFAULT ''::text NOT NULL,
    watch_channel_id text DEFAULT ''::text NOT NULL,
    watch_resource_id text DEFAULT ''::text NOT NULL,
    watch_token_hash text DEFAULT ''::text NOT NULL,
    watch_expires_at timestamp with time zone,
    status text DEFAULT 'pending'::text NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    last_reconciled_at timestamp with time zone,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    execution_owner text DEFAULT 'go'::text NOT NULL,
    native_sync_requested bigint DEFAULT 0 NOT NULL,
    native_sync_completed bigint DEFAULT 0 NOT NULL,
    native_sync_available_at timestamp with time zone DEFAULT now() NOT NULL,
    native_sync_lease_id uuid,
    native_sync_lease_until timestamp with time zone,
    CONSTRAINT space_calendar_sources_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 240))),
    CONSTRAINT space_calendar_sources_execution_owner_check CHECK ((execution_owner = ANY (ARRAY['go'::text, 'hono'::text]))),
    CONSTRAINT space_calendar_sources_native_sync_completed_check CHECK ((native_sync_completed >= 0)),
    CONSTRAINT space_calendar_sources_native_sync_requested_check CHECK ((native_sync_requested >= 0)),
    CONSTRAINT space_calendar_sources_provider_check CHECK ((provider = 'google'::text)),
    CONSTRAINT space_calendar_sources_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'syncing'::text, 'active'::text, 'needs_attention'::text, 'disabled'::text]))),
    CONSTRAINT space_calendar_sources_timezone_check CHECK (((char_length(timezone) >= 1) AND (char_length(timezone) <= 80)))
);

ALTER TABLE ONLY public.space_calendar_sources FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_conversation_members (
    conversation_id text NOT NULL,
    user_id text,
    joined_at timestamp with time zone DEFAULT now() NOT NULL,
    agent_id text,
    actor_kind text DEFAULT 'person'::text NOT NULL,
    CONSTRAINT space_conversation_member_actor_check CHECK ((((actor_kind = 'person'::text) AND (user_id IS NOT NULL) AND (agent_id IS NULL)) OR ((actor_kind = 'agent'::text) AND (user_id IS NULL) AND (agent_id IS NOT NULL)))),
    CONSTRAINT space_conversation_members_actor_kind_check CHECK ((actor_kind = ANY (ARRAY['person'::text, 'agent'::text])))
);

ALTER TABLE ONLY public.space_conversation_members FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_conversation_reads (
    conversation_id text NOT NULL,
    user_id text NOT NULL,
    read_message_seq bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_conversation_reads FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_conversations (
    id text NOT NULL,
    space_id text NOT NULL,
    title text NOT NULL,
    created_by_user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    origin text DEFAULT 'misty'::text NOT NULL,
    integration_id text,
    external_resource_id text DEFAULT ''::text NOT NULL,
    external_display_name text DEFAULT ''::text NOT NULL,
    integration_status text DEFAULT 'active'::text NOT NULL,
    visible_to_space boolean DEFAULT false NOT NULL,
    kind text DEFAULT 'standard'::text NOT NULL,
    direct_user_id text,
    direct_agent_id text,
    CONSTRAINT space_conversations_direct_actor_check CHECK ((((kind = 'direct'::text) AND (direct_user_id IS NOT NULL) AND (direct_agent_id IS NOT NULL)) OR ((kind <> 'direct'::text) AND (direct_user_id IS NULL) AND (direct_agent_id IS NULL)))),
    CONSTRAINT space_conversations_integration_status_check CHECK ((integration_status = ANY (ARRAY['active'::text, 'disconnected'::text]))),
    CONSTRAINT space_conversations_kind_check CHECK ((kind = ANY (ARRAY['standard'::text, 'direct'::text]))),
    CONSTRAINT space_conversations_origin_check CHECK ((origin = ANY (ARRAY['misty'::text, 'discord'::text, 'instagram'::text, 'slack'::text]))),
    CONSTRAINT space_conversations_title_check CHECK (((char_length(title) >= 1) AND (char_length(title) <= 80)))
);

ALTER TABLE ONLY public.space_conversations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_creation_requests (
    user_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint text NOT NULL,
    space_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_creation_requests FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_device_presence (
    owner_user_id text NOT NULL,
    device_id text NOT NULL,
    space_id text NOT NULL,
    app_id text NOT NULL,
    installed_version text NOT NULL,
    authority_generation bigint NOT NULL,
    endpoint_id text NOT NULL,
    addressing jsonb NOT NULL,
    protocol_version text NOT NULL,
    connection_hint text NOT NULL,
    last_heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_device_presence_addressing_check CHECK ((jsonb_typeof(addressing) = 'object'::text)),
    CONSTRAINT space_device_presence_app_id_check CHECK ((app_id = 'files'::text)),
    CONSTRAINT space_device_presence_authority_generation_check CHECK ((authority_generation > 0)),
    CONSTRAINT space_device_presence_connection_hint_check CHECK ((connection_hint = ANY (ARRAY['unknown'::text, 'direct'::text, 'relay'::text]))),
    CONSTRAINT space_device_presence_protocol_version_check CHECK ((protocol_version = 'misty-device/2'::text))
);

ALTER TABLE ONLY public.space_device_presence FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_discord_links (
    id text NOT NULL,
    space_id text NOT NULL,
    integration_id text NOT NULL,
    conversation_id text,
    connected_by_user_id text NOT NULL,
    guild_id text NOT NULL,
    guild_name text DEFAULT ''::text NOT NULL,
    channel_id text NOT NULL,
    channel_name text DEFAULT ''::text NOT NULL,
    direction text DEFAULT 'two_way'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    last_message_id text DEFAULT ''::text NOT NULL,
    last_synced_at timestamp with time zone,
    last_error_code text DEFAULT ''::text NOT NULL,
    bot_user_id text DEFAULT ''::text NOT NULL,
    webhook_id text DEFAULT ''::text NOT NULL,
    webhook_token_ciphertext bytea,
    webhook_token_nonce bytea,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_discord_links_channel_id_check CHECK (((char_length(channel_id) >= 1) AND (char_length(channel_id) <= 64))),
    CONSTRAINT space_discord_links_channel_name_check CHECK ((char_length(channel_name) <= 240)),
    CONSTRAINT space_discord_links_direction_check CHECK ((direction = ANY (ARRAY['two_way'::text, 'inbound'::text, 'outbound'::text]))),
    CONSTRAINT space_discord_links_guild_id_check CHECK (((char_length(guild_id) >= 1) AND (char_length(guild_id) <= 64))),
    CONSTRAINT space_discord_links_guild_name_check CHECK ((char_length(guild_name) <= 240)),
    CONSTRAINT space_discord_links_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'syncing'::text, 'active'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.space_discord_links FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_drawing_assets (
    id text NOT NULL,
    drawing_id text NOT NULL,
    file_id text NOT NULL,
    uploader_user_id text NOT NULL,
    excalidraw_file_id text NOT NULL,
    display_name text NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT space_drawing_assets_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
    CONSTRAINT space_drawing_assets_excalidraw_file_id_check CHECK (((char_length(excalidraw_file_id) >= 1) AND (char_length(excalidraw_file_id) <= 160))),
    CONSTRAINT space_drawing_assets_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'unreferenced'::text, 'deleting'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_drawing_assets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_drawing_control_outbox (
    id text NOT NULL,
    drawing_id text NOT NULL,
    command text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    delivered_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_drawing_control_outbox_command_check CHECK ((command = ANY (ARRAY['acl'::text, 'disconnect'::text, 'purge'::text])))
);

ALTER TABLE ONLY public.space_drawing_control_outbox FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_drawings (
    id text NOT NULL,
    space_id text NOT NULL,
    creator_user_id text NOT NULL,
    title text DEFAULT 'Untitled drawing'::text NOT NULL,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    collaboration_revision bigint DEFAULT 0 NOT NULL,
    acl_version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    audience_kind text DEFAULT 'space'::text NOT NULL,
    audience_conversation_id text,
    CONSTRAINT space_drawings_audience_check CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL)))),
    CONSTRAINT space_drawings_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT space_drawings_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'deleting'::text]))),
    CONSTRAINT space_drawings_title_check CHECK (((char_length(title) >= 1) AND (char_length(title) <= 200)))
);

ALTER TABLE ONLY public.space_drawings FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_events (
    id bigint NOT NULL,
    space_id text NOT NULL,
    event_type text NOT NULL,
    actor_user_id text,
    entity_id text,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_events FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.space_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.space_events_id_seq OWNED BY public.space_events.id;

CREATE TABLE public.space_inbox_items (
    id bigint NOT NULL,
    user_id text NOT NULL,
    space_id text NOT NULL,
    kind text NOT NULL,
    message_id text,
    event_id bigint,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    seen_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_inbox_items_kind_check CHECK ((kind = ANY (ARRAY['unread'::text, 'mention'::text, 'agent'::text, 'approval'::text, 'workflow'::text])))
);

ALTER TABLE ONLY public.space_inbox_items FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.space_inbox_items_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.space_inbox_items_id_seq OWNED BY public.space_inbox_items.id;

CREATE TABLE public.space_integrations (
    id text NOT NULL,
    space_id text NOT NULL,
    provider text NOT NULL,
    display_name text NOT NULL,
    credential_reference text NOT NULL,
    granted_permissions jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    connected_by_user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_integrations_granted_permissions_check CHECK ((jsonb_typeof(granted_permissions) = 'array'::text)),
    CONSTRAINT space_integrations_status_check CHECK ((status = ANY (ARRAY['active'::text, 'needs_attention'::text, 'disabled'::text])))
);

ALTER TABLE ONLY public.space_integrations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_invitation_delivery_jobs (
    invite_id text NOT NULL,
    generation uuid NOT NULL,
    token_key_id text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_id uuid,
    lease_expires_at timestamp with time zone,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_invitation_delivery_jobs_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT space_invitation_delivery_jobs_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'processing'::text, 'sent'::text, 'superseded'::text])))
);

ALTER TABLE ONLY public.space_invitation_delivery_jobs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_invitations (
    id text NOT NULL,
    space_id text NOT NULL,
    invited_user_id text,
    invited_by_user_id text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    invited_email text NOT NULL,
    token_hash text NOT NULL,
    delivery_status text DEFAULT 'pending'::text NOT NULL,
    revoked_at timestamp with time zone,
    consumed_at timestamp with time zone,
    last_sent_at timestamp with time zone,
    CONSTRAINT space_invitations_delivery_status_check CHECK ((delivery_status = ANY (ARRAY['pending'::text, 'sent'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.space_invitations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_item_aliases (
    id text NOT NULL,
    space_id text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    alias text NOT NULL,
    normalized_alias text NOT NULL,
    created_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_item_aliases_alias_check CHECK ((alias ~ '^[a-z0-9][a-z0-9_-]{2,63}$'::text)),
    CONSTRAINT space_item_aliases_check CHECK ((normalized_alias = lower(alias))),
    CONSTRAINT space_item_aliases_target_kind_check CHECK ((target_kind = ANY (ARRAY['library_item'::text, 'attachment'::text, 'album'::text, 'group'::text, 'person'::text, 'system_collection'::text, 'direct_reference'::text])))
);

ALTER TABLE ONLY public.space_item_aliases FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_asset_stack_members (
    stack_id text NOT NULL,
    space_library_item_id text NOT NULL,
    role text NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_asset_stack_members_position_check CHECK (("position" >= 0)),
    CONSTRAINT space_library_asset_stack_members_role_check CHECK ((role = ANY (ARRAY['still'::text, 'motion'::text, 'raw'::text, 'alternate'::text, 'burst_frame'::text])))
);

ALTER TABLE ONLY public.space_library_asset_stack_members FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_asset_stacks (
    id text NOT NULL,
    space_id text NOT NULL,
    kind text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    cover_item_id text NOT NULL,
    motion_item_id text,
    created_by_user_id text NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    effect text DEFAULT 'still'::text NOT NULL,
    CONSTRAINT space_library_asset_stacks_effect_check CHECK ((effect = ANY (ARRAY['still'::text, 'loop'::text, 'bounce'::text, 'long_exposure'::text]))),
    CONSTRAINT space_library_asset_stacks_kind_check CHECK ((kind = ANY (ARRAY['live_photo'::text, 'raw_pair'::text, 'burst'::text]))),
    CONSTRAINT space_library_asset_stacks_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'deleted'::text]))),
    CONSTRAINT space_library_asset_stacks_title_check CHECK ((char_length(title) <= 160))
);

ALTER TABLE ONLY public.space_library_asset_stacks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_audit_events (
    id bigint NOT NULL,
    request_id text NOT NULL,
    security_domain_id text,
    space_id text,
    actor_user_id text,
    actor_kind text DEFAULT 'user'::text NOT NULL,
    action text NOT NULL,
    target_kind text NOT NULL,
    target_id text,
    outcome text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_audit_events_actor_kind_check CHECK ((actor_kind = ANY (ARRAY['user'::text, 'service'::text, 'system'::text]))),
    CONSTRAINT space_library_audit_events_outcome_check CHECK ((outcome = ANY (ARRAY['success'::text, 'denied'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.space_library_audit_events FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.space_library_audit_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.space_library_audit_events_id_seq OWNED BY public.space_library_audit_events.id;

CREATE TABLE public.space_library_direct_references (
    id text NOT NULL,
    destination_space_id text NOT NULL,
    grant_id text NOT NULL,
    created_by_user_id text NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_direct_references_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'unavailable'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_library_direct_references FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_grants (
    id text NOT NULL,
    source_space_id text NOT NULL,
    source_item_id text NOT NULL,
    destination_space_id text NOT NULL,
    granted_by_user_id text NOT NULL,
    capabilities jsonb DEFAULT '["view"]'::jsonb NOT NULL,
    metadata_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_grants_check CHECK ((source_space_id <> destination_space_id)),
    CONSTRAINT space_library_grants_state_check CHECK ((state = ANY (ARRAY['active'::text, 'revoked'::text, 'expired'::text])))
);

ALTER TABLE ONLY public.space_library_grants FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_groups (
    id text NOT NULL,
    space_id text NOT NULL,
    name text NOT NULL,
    rules jsonb DEFAULT '{"all": []}'::jsonb NOT NULL,
    created_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_groups_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 120))),
    CONSTRAINT space_library_groups_rules_check CHECK ((octet_length((rules)::text) <= 16384))
);

ALTER TABLE ONLY public.space_library_groups FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_imports (
    id text NOT NULL,
    source_space_id text NOT NULL,
    source_item_id text NOT NULL,
    source_security_domain_id text NOT NULL,
    destination_space_id text NOT NULL,
    destination_item_id text,
    destination_security_domain_id text NOT NULL,
    importer_user_id text NOT NULL,
    quota_reservation_upload_id text,
    logical_bytes bigint NOT NULL,
    copy_policy text DEFAULT 'physical_destination_copy'::text NOT NULL,
    state text DEFAULT 'reserved'::text NOT NULL,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    CONSTRAINT space_library_imports_copy_policy_check CHECK ((copy_policy = 'physical_destination_copy'::text)),
    CONSTRAINT space_library_imports_logical_bytes_check CHECK ((logical_bytes > 0)),
    CONSTRAINT space_library_imports_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'copying'::text, 'processing'::text, 'ready'::text, 'failed'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_library_imports FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_intelligence_policies (
    space_id text NOT NULL,
    faces_enabled boolean DEFAULT false NOT NULL,
    pets_enabled boolean DEFAULT false NOT NULL,
    enabled_by_user_id text,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    ai_enabled boolean DEFAULT false NOT NULL,
    semantic_search_enabled boolean DEFAULT false NOT NULL
);

ALTER TABLE ONLY public.space_library_intelligence_policies FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_item_views (
    space_id text NOT NULL,
    space_library_item_id text NOT NULL,
    user_id text NOT NULL,
    view_count bigint DEFAULT 1 NOT NULL,
    first_viewed_at timestamp with time zone DEFAULT now() NOT NULL,
    last_viewed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_item_views_view_count_check CHECK ((view_count > 0))
);

ALTER TABLE ONLY public.space_library_item_views FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_items (
    id text NOT NULL,
    space_id text NOT NULL,
    file_id text NOT NULL,
    contributing_user_id text NOT NULL,
    display_name text NOT NULL,
    caption text DEFAULT ''::text NOT NULL,
    tags jsonb DEFAULT '[]'::jsonb NOT NULL,
    favorite boolean DEFAULT false NOT NULL,
    hidden boolean DEFAULT false NOT NULL,
    date_override timestamp with time zone,
    location_override jsonb,
    contributor_information jsonb DEFAULT '{}'::jsonb NOT NULL,
    current_edit_version_id text,
    added_by_user_id text NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    trashed_at timestamp with time zone,
    recover_until timestamp with time zone,
    version bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    audience_kind text DEFAULT 'space'::text NOT NULL,
    audience_conversation_id text,
    CONSTRAINT space_library_items_audience_check CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL)))),
    CONSTRAINT space_library_items_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT space_library_items_caption_check CHECK ((char_length(caption) <= 4000)),
    CONSTRAINT space_library_items_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
    CONSTRAINT space_library_items_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'trash'::text, 'purging'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_library_items FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_search_documents (
    space_id text NOT NULL,
    space_library_item_id text NOT NULL,
    security_domain_id text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    search_text text DEFAULT ''::text NOT NULL,
    search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, ((search_text || ' '::text) || (metadata)::text))) STORED,
    embedding public.vector(768),
    embedding_model text,
    embedding_version integer DEFAULT 0 NOT NULL,
    state text DEFAULT 'processing'::text NOT NULL,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_library_search_documents_state_check CHECK ((state = ANY (ARRAY['processing'::text, 'ready'::text, 'failed'::text])))
);

ALTER TABLE ONLY public.space_library_search_documents FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_library_uploads (
    id text NOT NULL,
    space_id text NOT NULL,
    security_domain_id text NOT NULL,
    user_id text NOT NULL,
    object_key text NOT NULL,
    original_filename text NOT NULL,
    purpose text NOT NULL,
    client_declared_mime_type text DEFAULT ''::text NOT NULL,
    requested_byte_size bigint NOT NULL,
    client_sha256 text NOT NULL,
    verified_byte_size bigint,
    verified_sha256 text,
    detected_mime_type text,
    state text NOT NULL,
    file_id text,
    upload_token_hash text NOT NULL,
    error_code text,
    expires_at timestamp with time zone NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    finalized_at timestamp with time zone,
    note_id text,
    drawing_id text,
    drawing_file_id text,
    conversation_id text,
    CONSTRAINT space_library_uploads_client_sha256_check CHECK ((client_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT space_library_uploads_drawing_file_id_ck CHECK (((drawing_file_id IS NULL) OR ((char_length(drawing_file_id) >= 1) AND (char_length(drawing_file_id) <= 160)))),
    CONSTRAINT space_library_uploads_drawing_purpose_ck CHECK (((purpose = 'drawing_attachment'::text) = ((drawing_id IS NOT NULL) AND (drawing_file_id IS NOT NULL)))),
    CONSTRAINT space_library_uploads_note_purpose_ck CHECK (((purpose = 'note_attachment'::text) = (note_id IS NOT NULL))),
    CONSTRAINT space_library_uploads_original_filename_check CHECK (((char_length(original_filename) >= 1) AND (char_length(original_filename) <= 255))),
    CONSTRAINT space_library_uploads_purpose_check CHECK ((purpose = ANY (ARRAY['library'::text, 'attachment'::text, 'note_attachment'::text, 'drawing_attachment'::text]))),
    CONSTRAINT space_library_uploads_requested_byte_size_check CHECK ((requested_byte_size > 0)),
    CONSTRAINT space_library_uploads_single_parent_ck CHECK ((NOT ((note_id IS NOT NULL) AND (drawing_id IS NOT NULL)))),
    CONSTRAINT space_library_uploads_state_check CHECK ((state = ANY (ARRAY['initiated'::text, 'uploading'::text, 'uploaded_unverified'::text, 'quarantined'::text, 'scanning'::text, 'processing'::text, 'ready'::text, 'rejected'::text, 'infected'::text, 'invalid'::text, 'expired'::text, 'processing_failed'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_library_uploads FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_member_permission_overrides (
    space_id text NOT NULL,
    user_id text NOT NULL,
    permission text NOT NULL,
    effect text NOT NULL,
    updated_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_member_permission_overrides_effect_check CHECK ((effect = ANY (ARRAY['allow'::text, 'deny'::text])))
);

ALTER TABLE ONLY public.space_member_permission_overrides FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_member_roles (
    space_id text NOT NULL,
    user_id text NOT NULL,
    role_id text NOT NULL,
    assigned_by_user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_member_roles FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_member_storage_usage (
    space_id text NOT NULL,
    user_id text NOT NULL,
    contributed_bytes bigint DEFAULT 0 NOT NULL,
    reserved_bytes bigint DEFAULT 0 NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_member_storage_usage_contributed_bytes_check CHECK ((contributed_bytes >= 0)),
    CONSTRAINT space_member_storage_usage_reserved_bytes_check CHECK ((reserved_bytes >= 0))
);

ALTER TABLE ONLY public.space_member_storage_usage FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_members (
    space_id text NOT NULL,
    user_id text NOT NULL,
    role text NOT NULL,
    read_message_seq bigint DEFAULT 0 NOT NULL,
    joined_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_members_role_check CHECK ((role = ANY (ARRAY['owner'::text, 'member'::text])))
);

ALTER TABLE ONLY public.space_members FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_memory_preferences (
    space_id text NOT NULL,
    memory_id text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    cover_item_id text,
    music_item_id text,
    playback_seconds real DEFAULT 4.5 NOT NULL,
    updated_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_memory_preferences_playback_seconds_check CHECK (((playback_seconds >= (1)::double precision) AND (playback_seconds <= (15)::double precision))),
    CONSTRAINT space_memory_preferences_title_check CHECK ((char_length(title) <= 160))
);

ALTER TABLE ONLY public.space_memory_preferences FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_message_attachments (
    id text NOT NULL,
    space_id text NOT NULL,
    message_id text,
    file_id text NOT NULL,
    upload_id text NOT NULL,
    uploader_user_id text NOT NULL,
    display_name text NOT NULL,
    promoted_item_id text,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    recover_until timestamp with time zone,
    CONSTRAINT space_message_attachments_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
    CONSTRAINT space_message_attachments_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'recovery'::text, 'purging'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_message_attachments FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_message_library_references (
    message_id text NOT NULL,
    space_id text NOT NULL,
    space_library_item_id text NOT NULL,
    created_by_user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_message_library_references FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_message_reactions (
    message_id text NOT NULL,
    space_id text NOT NULL,
    user_id text NOT NULL,
    emoji text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_message_reactions_emoji_check CHECK ((((char_length(emoji) >= 1) AND (char_length(emoji) <= 8)) AND (octet_length(emoji) <= 32)))
);

ALTER TABLE ONLY public.space_message_reactions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_messages (
    seq bigint NOT NULL,
    id text NOT NULL,
    space_id text NOT NULL,
    sender_user_id text NOT NULL,
    sender_kind text DEFAULT 'person'::text NOT NULL,
    sender_agent_id text,
    content jsonb DEFAULT '[]'::jsonb NOT NULL,
    file_node_ids text[] DEFAULT '{}'::text[] NOT NULL,
    edited_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone DEFAULT (now() + '30 days'::interval),
    reply_to_message_id text,
    conversation_id text,
    origin jsonb,
    social_provider text,
    social_external_id text DEFAULT ''::text NOT NULL,
    social_external_conversation_id text DEFAULT ''::text NOT NULL,
    social_identity_id text,
    social_direction text,
    social_delivery_state text,
    CONSTRAINT space_messages_origin_check CHECK (((origin IS NULL) OR (jsonb_typeof(origin) = 'object'::text))),
    CONSTRAINT space_messages_sender_kind_check CHECK ((sender_kind = ANY (ARRAY['person'::text, 'agent'::text, 'system'::text]))),
    CONSTRAINT space_messages_social_delivery_state_check CHECK (((social_delivery_state IS NULL) OR (social_delivery_state = ANY (ARRAY['queued'::text, 'sending'::text, 'sent'::text, 'delivered'::text, 'read'::text, 'failed'::text, 'cancelled'::text])))),
    CONSTRAINT space_messages_social_direction_check CHECK (((social_direction IS NULL) OR (social_direction = ANY (ARRAY['inbound'::text, 'outbound'::text])))),
    CONSTRAINT space_messages_social_external_conversation_id_check CHECK ((char_length(social_external_conversation_id) <= 320)),
    CONSTRAINT space_messages_social_external_id_check CHECK ((char_length(social_external_id) <= 320)),
    CONSTRAINT space_messages_social_provider_check CHECK (((social_provider IS NULL) OR (social_provider = ANY (ARRAY['misty'::text, 'discord'::text, 'instagram'::text]))))
);

ALTER TABLE ONLY public.space_messages FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE public.space_messages_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.space_messages_seq_seq OWNED BY public.space_messages.seq;

CREATE TABLE public.space_native_calendar_events (
    id text NOT NULL,
    space_id text NOT NULL,
    title text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    location text DEFAULT ''::text NOT NULL,
    starts_at timestamp with time zone NOT NULL,
    ends_at timestamp with time zone NOT NULL,
    all_day boolean DEFAULT false NOT NULL,
    timezone text DEFAULT 'UTC'::text NOT NULL,
    status text DEFAULT 'confirmed'::text NOT NULL,
    audience_kind text DEFAULT 'space'::text NOT NULL,
    audience_conversation_id text,
    created_by_user_id text NOT NULL,
    created_by_agent_id text,
    source_run_id text,
    version bigint DEFAULT 1 NOT NULL,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_native_calendar_events_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT space_native_calendar_events_check CHECK ((ends_at >= starts_at)),
    CONSTRAINT space_native_calendar_events_check1 CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL)))),
    CONSTRAINT space_native_calendar_events_description_check CHECK ((char_length(description) <= 20000)),
    CONSTRAINT space_native_calendar_events_location_check CHECK ((char_length(location) <= 1000)),
    CONSTRAINT space_native_calendar_events_status_check CHECK ((status = ANY (ARRAY['confirmed'::text, 'tentative'::text, 'canceled'::text]))),
    CONSTRAINT space_native_calendar_events_timezone_check CHECK (((char_length(timezone) >= 1) AND (char_length(timezone) <= 80))),
    CONSTRAINT space_native_calendar_events_title_check CHECK (((char_length(btrim(title)) >= 1) AND (char_length(btrim(title)) <= 240))),
    CONSTRAINT space_native_calendar_events_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.space_native_calendar_events FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_nodes (
    id text NOT NULL,
    space_id text NOT NULL,
    parent_id text,
    kind text NOT NULL,
    display_name text NOT NULL,
    uploader_user_id text NOT NULL,
    target_ciphertext bytea,
    target_nonce bytea,
    target_key_version smallint,
    mime_type text DEFAULT ''::text NOT NULL,
    size_bytes bigint,
    stale boolean DEFAULT false NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_nodes_check CHECK ((((kind = 'folder'::text) AND (target_ciphertext IS NULL) AND (target_nonce IS NULL)) OR ((kind = 'link'::text) AND (target_ciphertext IS NOT NULL) AND (target_nonce IS NOT NULL)))),
    CONSTRAINT space_nodes_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
    CONSTRAINT space_nodes_kind_check CHECK ((kind = ANY (ARRAY['folder'::text, 'link'::text]))),
    CONSTRAINT space_nodes_size_bytes_check CHECK (((size_bytes IS NULL) OR (size_bytes >= 0)))
);

ALTER TABLE ONLY public.space_nodes FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_note_assets (
    id text NOT NULL,
    note_id text NOT NULL,
    file_id text NOT NULL,
    uploader_user_id text NOT NULL,
    display_name text NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT space_note_assets_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
    CONSTRAINT space_note_assets_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'unreferenced'::text, 'deleting'::text, 'deleted'::text])))
);

ALTER TABLE ONLY public.space_note_assets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_note_control_outbox (
    id text NOT NULL,
    note_id text NOT NULL,
    command text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    delivered_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_note_control_outbox_command_check CHECK ((command = ANY (ARRAY['acl'::text, 'disconnect'::text, 'purge'::text, 'bootstrap'::text, 'replace_markdown'::text])))
);

ALTER TABLE ONLY public.space_note_control_outbox FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_note_links (
    source_note_id text NOT NULL,
    target_note_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_note_links_check CHECK ((source_note_id <> target_note_id))
);

ALTER TABLE ONLY public.space_note_links FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_note_permissions (
    note_id text NOT NULL,
    user_id text NOT NULL,
    role text NOT NULL,
    granted_by text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_note_permissions_role_check CHECK ((role = ANY (ARRAY['viewer'::text, 'editor'::text])))
);

ALTER TABLE ONLY public.space_note_permissions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_notes (
    id text NOT NULL,
    space_id text NOT NULL,
    creator_user_id text NOT NULL,
    title_projection text DEFAULT ''::text NOT NULL,
    plain_text_projection text DEFAULT ''::text NOT NULL,
    shared_tags jsonb DEFAULT '[]'::jsonb NOT NULL,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    archived_at timestamp with time zone,
    purge_after timestamp with time zone,
    collaboration_revision bigint DEFAULT 0 NOT NULL,
    acl_version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    audience_kind text DEFAULT 'space'::text NOT NULL,
    audience_conversation_id text,
    markdown_projection text DEFAULT ''::text NOT NULL,
    CONSTRAINT space_notes_archive_timestamp_check CHECK ((((lifecycle_state = 'archived'::text) AND (archived_at IS NOT NULL) AND (purge_after IS NULL)) OR (lifecycle_state <> 'archived'::text))),
    CONSTRAINT space_notes_audience_check CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL)))),
    CONSTRAINT space_notes_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT space_notes_creator_left_timestamp_check CHECK (((lifecycle_state <> 'archived_creator_left'::text) OR ((archived_at IS NOT NULL) AND (purge_after IS NOT NULL)))),
    CONSTRAINT space_notes_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'archived'::text, 'archived_creator_left'::text, 'deleting'::text]))),
    CONSTRAINT space_notes_markdown_projection_check CHECK ((char_length(markdown_projection) <= 100000)),
    CONSTRAINT space_notes_plain_text_projection_check CHECK ((char_length(plain_text_projection) <= 100000))
);

ALTER TABLE ONLY public.space_notes FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_people (
    id text NOT NULL,
    space_id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    cover_item_id text,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    kind text DEFAULT 'person'::text NOT NULL,
    created_by_user_id text,
    merged_into_id text,
    automatic_centroid jsonb,
    automatic_sample_count integer DEFAULT 0 NOT NULL,
    CONSTRAINT space_people_automatic_centroid_size CHECK (((automatic_centroid IS NULL) OR (octet_length((automatic_centroid)::text) <= 65536))),
    CONSTRAINT space_people_automatic_sample_count_check CHECK ((automatic_sample_count >= 0)),
    CONSTRAINT space_people_kind_check CHECK ((kind = ANY (ARRAY['person'::text, 'pet'::text]))),
    CONSTRAINT space_people_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'merged'::text, 'deleted'::text]))),
    CONSTRAINT space_people_name_check CHECK ((char_length(name) <= 120))
);

ALTER TABLE ONLY public.space_people FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_person_observations (
    person_id text NOT NULL,
    space_library_item_id text NOT NULL,
    derivative_id text,
    confidence real NOT NULL,
    bounds jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    id text NOT NULL,
    source text DEFAULT 'automatic'::text NOT NULL,
    CONSTRAINT space_person_observations_confidence_check CHECK (((confidence >= (0)::double precision) AND (confidence <= (1)::double precision))),
    CONSTRAINT space_person_observations_source_check CHECK ((source = ANY (ARRAY['automatic'::text, 'manual'::text])))
);

ALTER TABLE ONLY public.space_person_observations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_pinned_collections (
    id text NOT NULL,
    space_id text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    "position" integer NOT NULL,
    pinned_by_user_id text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_pinned_collections_position_check CHECK ((("position" >= 0) AND ("position" <= 99))),
    CONSTRAINT space_pinned_collections_target_id_check CHECK (((char_length(target_id) >= 1) AND (char_length(target_id) <= 255))),
    CONSTRAINT space_pinned_collections_target_kind_check CHECK ((target_kind = ANY (ARRAY['system'::text, 'album'::text, 'group'::text, 'person'::text, 'memory'::text, 'trip'::text, 'map'::text])))
);

ALTER TABLE ONLY public.space_pinned_collections FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_provider_credentials (
    id text NOT NULL,
    integration_id text NOT NULL,
    space_id text NOT NULL,
    user_id text NOT NULL,
    provider text NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version smallint DEFAULT 1 NOT NULL,
    account_id text DEFAULT ''::text NOT NULL,
    account_display text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone,
    last_refreshed_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_provider_credentials FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_rendition_reservations (
    id text NOT NULL,
    space_id text NOT NULL,
    user_id text NOT NULL,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    reserved_bytes bigint NOT NULL,
    state text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_rendition_reservations_reserved_bytes_check CHECK ((reserved_bytes > 0)),
    CONSTRAINT space_rendition_reservations_source_kind_check CHECK ((source_kind = ANY (ARRAY['edit'::text, 'export'::text, 'preview'::text]))),
    CONSTRAINT space_rendition_reservations_state_check CHECK ((state = ANY (ARRAY['active'::text, 'consumed'::text, 'released'::text])))
);

ALTER TABLE ONLY public.space_rendition_reservations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_resolve_tickets (
    token_hash text NOT NULL,
    user_id text NOT NULL,
    space_id text NOT NULL,
    node_id text NOT NULL,
    disposition text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_resolve_tickets_disposition_check CHECK ((disposition = ANY (ARRAY['open'::text, 'download'::text])))
);

ALTER TABLE ONLY public.space_resolve_tickets FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmap_edges (
    id text NOT NULL,
    space_id text NOT NULL,
    roadmap_id text NOT NULL,
    source_goal_id text,
    target_goal_id text,
    edge_type text NOT NULL,
    label text DEFAULT ''::text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source_kind text DEFAULT 'goal'::text NOT NULL,
    source_id text NOT NULL,
    target_kind text DEFAULT 'goal'::text NOT NULL,
    target_id text NOT NULL,
    CONSTRAINT space_roadmap_edges_check CHECK ((source_goal_id <> target_goal_id)),
    CONSTRAINT space_roadmap_edges_distinct_endpoints_check CHECK (((source_kind <> target_kind) OR (source_id <> target_id))),
    CONSTRAINT space_roadmap_edges_edge_type_check CHECK ((edge_type = ANY (ARRAY['depends_on'::text, 'dependency'::text, 'blocks'::text, 'enables'::text, 'contributes_to'::text, 'measures'::text, 'documents'::text, 'related'::text]))),
    CONSTRAINT space_roadmap_edges_label_check CHECK ((char_length(label) <= 120)),
    CONSTRAINT space_roadmap_edges_source_kind_check CHECK ((source_kind = ANY (ARRAY['milestone'::text, 'goal'::text, 'node'::text]))),
    CONSTRAINT space_roadmap_edges_target_kind_check CHECK ((target_kind = ANY (ARRAY['milestone'::text, 'goal'::text, 'node'::text]))),
    CONSTRAINT space_roadmap_edges_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.space_roadmap_edges FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmap_goal_tasks (
    space_id text NOT NULL,
    roadmap_id text NOT NULL,
    goal_id text NOT NULL,
    task_id text NOT NULL,
    added_by_user_id text NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_roadmap_goal_tasks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmap_goals (
    id text NOT NULL,
    space_id text NOT NULL,
    roadmap_id text NOT NULL,
    milestone_id text NOT NULL,
    title text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    target_date date,
    rank bigint NOT NULL,
    position_x double precision DEFAULT 24 NOT NULL,
    position_y double precision DEFAULT 72 NOT NULL,
    manual_completed_at timestamp with time zone,
    manual_completed_by_user_id text,
    version bigint DEFAULT 1 NOT NULL,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_roadmap_goals_check CHECK (((manual_completed_at IS NULL) = (manual_completed_by_user_id IS NULL))),
    CONSTRAINT space_roadmap_goals_description_check CHECK ((char_length(description) <= 20000)),
    CONSTRAINT space_roadmap_goals_position_x_check CHECK ((abs(position_x) <= (10000000)::double precision)),
    CONSTRAINT space_roadmap_goals_position_y_check CHECK ((abs(position_y) <= (10000000)::double precision)),
    CONSTRAINT space_roadmap_goals_rank_check CHECK ((rank > 0)),
    CONSTRAINT space_roadmap_goals_title_check CHECK (((char_length(btrim(title)) >= 1) AND (char_length(btrim(title)) <= 240))),
    CONSTRAINT space_roadmap_goals_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.space_roadmap_goals FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmap_milestones (
    id text NOT NULL,
    space_id text NOT NULL,
    roadmap_id text NOT NULL,
    title text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    target_date date,
    rank bigint NOT NULL,
    position_x double precision DEFAULT 0 NOT NULL,
    position_y double precision DEFAULT 0 NOT NULL,
    width double precision DEFAULT 440 NOT NULL,
    height double precision DEFAULT 360 NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_roadmap_milestones_description_check CHECK ((char_length(description) <= 10000)),
    CONSTRAINT space_roadmap_milestones_height_check CHECK (((height >= (220)::double precision) AND (height <= (2400)::double precision))),
    CONSTRAINT space_roadmap_milestones_position_x_check CHECK ((abs(position_x) <= (10000000)::double precision)),
    CONSTRAINT space_roadmap_milestones_position_y_check CHECK ((abs(position_y) <= (10000000)::double precision)),
    CONSTRAINT space_roadmap_milestones_rank_check CHECK ((rank > 0)),
    CONSTRAINT space_roadmap_milestones_title_check CHECK (((char_length(btrim(title)) >= 1) AND (char_length(btrim(title)) <= 200))),
    CONSTRAINT space_roadmap_milestones_version_check CHECK ((version > 0)),
    CONSTRAINT space_roadmap_milestones_width_check CHECK (((width >= (280)::double precision) AND (width <= (2400)::double precision)))
);

ALTER TABLE ONLY public.space_roadmap_milestones FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmap_node_definitions (
    id text NOT NULL,
    space_id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    icon text DEFAULT 'shapes'::text NOT NULL,
    color text DEFAULT 'slate'::text NOT NULL,
    agenda_visible boolean DEFAULT false NOT NULL,
    field_schema jsonb DEFAULT '[]'::jsonb NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_by_user_id text NOT NULL,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_roadmap_node_definitions_color_check CHECK ((color = ANY (ARRAY['slate'::text, 'blue'::text, 'cyan'::text, 'emerald'::text, 'amber'::text, 'orange'::text, 'rose'::text, 'violet'::text]))),
    CONSTRAINT space_roadmap_node_definitions_description_check CHECK ((char_length(description) <= 2000)),
    CONSTRAINT space_roadmap_node_definitions_field_schema_check CHECK ((jsonb_typeof(field_schema) = 'array'::text)),
    CONSTRAINT space_roadmap_node_definitions_icon_check CHECK (((char_length(icon) >= 1) AND (char_length(icon) <= 80))),
    CONSTRAINT space_roadmap_node_definitions_name_check CHECK (((char_length(btrim(name)) >= 1) AND (char_length(btrim(name)) <= 120))),
    CONSTRAINT space_roadmap_node_definitions_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.space_roadmap_node_definitions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmap_nodes (
    id text NOT NULL,
    space_id text NOT NULL,
    roadmap_id text NOT NULL,
    milestone_id text,
    definition_id text,
    node_kind text NOT NULL,
    title text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    target_date date,
    position_x double precision DEFAULT 0 NOT NULL,
    position_y double precision DEFAULT 0 NOT NULL,
    field_values jsonb DEFAULT '{}'::jsonb NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_roadmap_nodes_check CHECK (((node_kind = 'custom'::text) = (definition_id IS NOT NULL))),
    CONSTRAINT space_roadmap_nodes_description_check CHECK ((char_length(description) <= 20000)),
    CONSTRAINT space_roadmap_nodes_field_values_check CHECK ((jsonb_typeof(field_values) = 'object'::text)),
    CONSTRAINT space_roadmap_nodes_node_kind_check CHECK ((node_kind = ANY (ARRAY['risk'::text, 'decision'::text, 'metric'::text, 'note'::text, 'custom'::text]))),
    CONSTRAINT space_roadmap_nodes_position_x_check CHECK ((abs(position_x) <= (10000000)::double precision)),
    CONSTRAINT space_roadmap_nodes_position_y_check CHECK ((abs(position_y) <= (10000000)::double precision)),
    CONSTRAINT space_roadmap_nodes_title_check CHECK (((char_length(btrim(title)) >= 1) AND (char_length(btrim(title)) <= 240))),
    CONSTRAINT space_roadmap_nodes_version_check CHECK ((version > 0))
);

ALTER TABLE ONLY public.space_roadmap_nodes FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roadmaps (
    id text NOT NULL,
    space_id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    graph_version bigint DEFAULT 1 NOT NULL,
    created_by_user_id text NOT NULL,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    audience_kind text DEFAULT 'space'::text NOT NULL,
    audience_conversation_id text,
    CONSTRAINT space_roadmaps_audience_check CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL)))),
    CONSTRAINT space_roadmaps_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT space_roadmaps_description_check CHECK ((char_length(description) <= 5000)),
    CONSTRAINT space_roadmaps_graph_version_check CHECK ((graph_version > 0)),
    CONSTRAINT space_roadmaps_name_check CHECK (((char_length(btrim(name)) >= 1) AND (char_length(btrim(name)) <= 160)))
);

ALTER TABLE ONLY public.space_roadmaps FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_roles (
    id text NOT NULL,
    space_id text NOT NULL,
    name text NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    is_everyone boolean DEFAULT false NOT NULL,
    permissions jsonb DEFAULT '[]'::jsonb NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_roles_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80)))
);

ALTER TABLE ONLY public.space_roles FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_run_actions (
    id text NOT NULL,
    run_id text NOT NULL,
    action_kind text NOT NULL,
    summary text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    destructive boolean DEFAULT false NOT NULL,
    state text DEFAULT 'proposed'::text NOT NULL,
    performed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_run_actions_details_check CHECK ((jsonb_typeof(details) = 'object'::text)),
    CONSTRAINT space_run_actions_state_check CHECK ((state = ANY (ARRAY['proposed'::text, 'approved'::text, 'completed'::text, 'failed'::text, 'canceled'::text])))
);

ALTER TABLE ONLY public.space_run_actions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_run_approvals (
    id text NOT NULL,
    run_id text NOT NULL,
    requested_from_user_id text NOT NULL,
    decided_by_user_id text,
    action_summary text NOT NULL,
    proposed_actions jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    decided_at timestamp with time zone,
    expires_at timestamp with time zone DEFAULT (now() + '24:00:00'::interval) NOT NULL,
    CONSTRAINT space_run_approvals_proposed_actions_check CHECK ((jsonb_typeof(proposed_actions) = 'array'::text)),
    CONSTRAINT space_run_approvals_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'expired'::text, 'canceled'::text])))
);

ALTER TABLE ONLY public.space_run_approvals FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_run_steps (
    id text NOT NULL,
    run_id text NOT NULL,
    node_id text NOT NULL,
    state text NOT NULL,
    attempt integer DEFAULT 1 NOT NULL,
    input jsonb DEFAULT '{}'::jsonb NOT NULL,
    output jsonb DEFAULT '{}'::jsonb NOT NULL,
    error_code text,
    error_message text,
    next_retry_at timestamp with time zone,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_run_steps_attempt_check CHECK (((attempt >= 1) AND (attempt <= 3))),
    CONSTRAINT space_run_steps_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'cooldown'::text, 'awaiting_approval'::text, 'completed'::text, 'completed_with_errors'::text, 'failed'::text, 'canceled'::text, 'rejected'::text])))
);

ALTER TABLE ONLY public.space_run_steps FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_runs (
    id text NOT NULL,
    space_id text,
    resource_kind text NOT NULL,
    resource_id text NOT NULL,
    initiated_by_user_id text NOT NULL,
    billing_user_id text NOT NULL,
    trigger_kind text NOT NULL,
    state text NOT NULL,
    input jsonb DEFAULT '{}'::jsonb NOT NULL,
    result jsonb DEFAULT '{}'::jsonb NOT NULL,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    requesting_member_id text NOT NULL,
    source_conversation_id text,
    source_type text DEFAULT 'direct'::text NOT NULL,
    agent_id text,
    workflow_identifier text,
    workflow_version_id text,
    workflow_version text,
    capability_id text,
    progress integer DEFAULT 0 NOT NULL,
    outputs jsonb DEFAULT '{}'::jsonb NOT NULL,
    artifacts jsonb DEFAULT '[]'::jsonb NOT NULL,
    error_message text,
    retry_of_run_id text,
    canceled_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    agent_instance_id text,
    agent_version_id text,
    attempt integer DEFAULT 1 NOT NULL,
    next_retry_at timestamp with time zone,
    source_task_id text,
    action_envelope jsonb DEFAULT '{}'::jsonb NOT NULL,
    conversation_scope_kind text DEFAULT 'everyone'::text NOT NULL,
    scope_conversation_id text,
    source_message_id text,
    runtime_kind text DEFAULT ''::text NOT NULL,
    runtime_run_id text DEFAULT ''::text NOT NULL,
    runtime_phase text DEFAULT ''::text NOT NULL,
    runtime_heartbeat_at timestamp with time zone,
    owner_user_id text NOT NULL,
    initial_run_mode text DEFAULT 'auto'::text NOT NULL,
    effective_run_mode text DEFAULT 'auto'::text NOT NULL,
    agent_version_snapshot jsonb DEFAULT '{}'::jsonb NOT NULL,
    approval_state text DEFAULT 'none'::text NOT NULL,
    parent_run_id text,
    delegation_depth integer DEFAULT 0 NOT NULL,
    context_bindings jsonb DEFAULT '[]'::jsonb NOT NULL,
    device_wait_hook_token text DEFAULT ''::text NOT NULL,
    device_wait_expires_at timestamp with time zone,
    source_agent_conversation_id text,
    execution_owner text DEFAULT 'go'::text NOT NULL,
    device_wait_scope_id text DEFAULT ''::text NOT NULL,
    device_wait_capability text DEFAULT ''::text NOT NULL,
    approval_wait_id text DEFAULT ''::text NOT NULL,
    model_budget_version integer DEFAULT 1 NOT NULL,
    model_turn_limit integer DEFAULT 20 NOT NULL,
    execution_budget_version integer DEFAULT 1 NOT NULL,
    execution_limit_ms bigint DEFAULT 1800000 NOT NULL,
    execution_consumed_ms bigint DEFAULT 0 NOT NULL,
    execution_active_at timestamp with time zone,
    runtime_adapter_version text DEFAULT 'vercel-workflow/1'::text NOT NULL,
    runtime_endpoint text,
    runtime_callback_endpoint text,
    CONSTRAINT space_runs_action_envelope_check CHECK ((jsonb_typeof(action_envelope) = 'object'::text)),
    CONSTRAINT space_runs_agent_version_snapshot_check CHECK ((jsonb_typeof(agent_version_snapshot) = 'object'::text)),
    CONSTRAINT space_runs_approval_state_check CHECK ((approval_state = ANY (ARRAY['none'::text, 'pending'::text, 'approved'::text, 'denied'::text, 'expired'::text]))),
    CONSTRAINT space_runs_artifacts_check CHECK ((jsonb_typeof(artifacts) = 'array'::text)),
    CONSTRAINT space_runs_attempt_check CHECK (((attempt >= 1) AND (attempt <= 3))),
    CONSTRAINT space_runs_context_bindings_check CHECK ((jsonb_typeof(context_bindings) = 'array'::text)),
    CONSTRAINT space_runs_conversation_scope_check CHECK ((((conversation_scope_kind = 'everyone'::text) AND (scope_conversation_id IS NULL)) OR ((conversation_scope_kind = 'conversation'::text) AND (scope_conversation_id IS NOT NULL)))),
    CONSTRAINT space_runs_conversation_scope_kind_check CHECK ((conversation_scope_kind = ANY (ARRAY['everyone'::text, 'conversation'::text]))),
    CONSTRAINT space_runs_delegation_depth_check CHECK (((delegation_depth >= 0) AND (delegation_depth <= 2))),
    CONSTRAINT space_runs_effective_run_mode_check CHECK ((effective_run_mode = ANY (ARRAY['ask'::text, 'auto'::text, 'full'::text]))),
    CONSTRAINT space_runs_execution_budget_version_check CHECK ((execution_budget_version = ANY (ARRAY[0, 1]))),
    CONSTRAINT space_runs_execution_consumed_ms_check CHECK ((execution_consumed_ms >= 0)),
    CONSTRAINT space_runs_execution_limit_ms_check CHECK (((execution_limit_ms >= 1) AND (execution_limit_ms <= 1800000))),
    CONSTRAINT space_runs_execution_owner_check CHECK ((execution_owner = ANY (ARRAY['go'::text, 'hono'::text]))),
    CONSTRAINT space_runs_initial_run_mode_check CHECK ((initial_run_mode = ANY (ARRAY['ask'::text, 'auto'::text, 'full'::text]))),
    CONSTRAINT space_runs_model_budget_version_check CHECK ((model_budget_version = ANY (ARRAY[0, 1]))),
    CONSTRAINT space_runs_model_turn_limit_check CHECK (((model_turn_limit >= 1) AND (model_turn_limit <= 20))),
    CONSTRAINT space_runs_outputs_check CHECK ((jsonb_typeof(outputs) = 'object'::text)),
    CONSTRAINT space_runs_progress_check CHECK (((progress >= 0) AND (progress <= 100))),
    CONSTRAINT space_runs_resource_kind_check CHECK ((resource_kind = 'agent'::text)),
    CONSTRAINT space_runs_source_type_check CHECK ((source_type = ANY (ARRAY['direct'::text, 'group_mention'::text, 'agent_console'::text, 'studio_test'::text, 'schedule'::text, 'connector'::text, 'task'::text, 'suggestion'::text, 'follow_up'::text]))),
    CONSTRAINT space_runs_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'awaiting_approval'::text, 'awaiting_device'::text, 'awaiting_intervention'::text, 'completed'::text, 'completed_with_errors'::text, 'failed'::text, 'canceled'::text, 'rejected'::text, 'cooldown'::text, 'retrying'::text])))
);

ALTER TABLE ONLY public.space_runs FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_setup_integrations (
    space_id text NOT NULL,
    provider text NOT NULL,
    status text DEFAULT 'selected'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_setup_integrations_provider_check CHECK ((provider = ANY (ARRAY['google'::text, 'discord'::text, 'notion'::text]))),
    CONSTRAINT space_setup_integrations_status_check CHECK ((status = ANY (ARRAY['selected'::text, 'authorized'::text, 'configured'::text, 'skipped'::text])))
);

ALTER TABLE ONLY public.space_setup_integrations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_slack_links (
    id text NOT NULL,
    space_id text NOT NULL,
    integration_id text NOT NULL,
    shared_resource_id text NOT NULL,
    conversation_id text NOT NULL,
    connected_by_user_id text NOT NULL,
    team_id text NOT NULL,
    team_name text DEFAULT ''::text NOT NULL,
    channel_id text NOT NULL,
    channel_name text DEFAULT ''::text NOT NULL,
    direction text DEFAULT 'two_way'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    last_message_ts text DEFAULT ''::text NOT NULL,
    last_synced_at timestamp with time zone,
    last_error_code text DEFAULT ''::text NOT NULL,
    bot_user_id text DEFAULT ''::text NOT NULL,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_slack_links_bot_user_id_check CHECK ((char_length(bot_user_id) <= 120)),
    CONSTRAINT space_slack_links_channel_id_check CHECK (((char_length(channel_id) >= 1) AND (char_length(channel_id) <= 120))),
    CONSTRAINT space_slack_links_channel_name_check CHECK ((char_length(channel_name) <= 240)),
    CONSTRAINT space_slack_links_direction_check CHECK ((direction = ANY (ARRAY['two_way'::text, 'inbound'::text, 'outbound'::text]))),
    CONSTRAINT space_slack_links_last_error_code_check CHECK ((char_length(last_error_code) <= 120)),
    CONSTRAINT space_slack_links_last_message_ts_check CHECK ((char_length(last_message_ts) <= 64)),
    CONSTRAINT space_slack_links_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'syncing'::text, 'active'::text, 'needs_attention'::text, 'disabled'::text]))),
    CONSTRAINT space_slack_links_team_id_check CHECK (((char_length(team_id) >= 1) AND (char_length(team_id) <= 120))),
    CONSTRAINT space_slack_links_team_name_check CHECK ((char_length(team_name) <= 240))
);

ALTER TABLE ONLY public.space_slack_links FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_storage_contributions (
    id text NOT NULL,
    space_id text NOT NULL,
    user_id text NOT NULL,
    file_id text,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    logical_bytes bigint NOT NULL,
    state text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    released_at timestamp with time zone,
    CONSTRAINT space_storage_contributions_logical_bytes_check CHECK ((logical_bytes > 0)),
    CONSTRAINT space_storage_contributions_source_kind_check CHECK ((source_kind = ANY (ARRAY['attachment'::text, 'library_item'::text, 'import'::text, 'duplicate'::text, 'edit'::text, 'export'::text, 'note_asset'::text, 'drawing_asset'::text]))),
    CONSTRAINT space_storage_contributions_state_check CHECK ((state = ANY (ARRAY['active'::text, 'recovery'::text, 'released'::text])))
);

ALTER TABLE ONLY public.space_storage_contributions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_storage_usage (
    space_id text NOT NULL,
    used_bytes bigint DEFAULT 0 NOT NULL,
    reserved_bytes bigint DEFAULT 0 NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_storage_usage_reserved_bytes_check CHECK ((reserved_bytes >= 0)),
    CONSTRAINT space_storage_usage_used_bytes_check CHECK ((used_bytes >= 0))
);

ALTER TABLE ONLY public.space_storage_usage FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_task_activity (
    id text NOT NULL,
    space_id text NOT NULL,
    task_id text NOT NULL,
    actor_kind text NOT NULL,
    actor_user_id text,
    actor_agent_id text,
    run_id text,
    kind text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_task_activity_actor_kind_check CHECK ((actor_kind = ANY (ARRAY['person'::text, 'agent'::text, 'system'::text]))),
    CONSTRAINT space_task_activity_check CHECK ((((actor_kind = 'person'::text) AND (actor_user_id IS NOT NULL) AND (actor_agent_id IS NULL)) OR ((actor_kind = 'agent'::text) AND (actor_agent_id IS NOT NULL) AND (actor_user_id IS NULL)) OR ((actor_kind = 'system'::text) AND (actor_user_id IS NULL) AND (actor_agent_id IS NULL)))),
    CONSTRAINT space_task_activity_kind_check CHECK ((kind = ANY (ARRAY['assigned'::text, 'progress'::text, 'result'::text, 'failure'::text, 'completed'::text, 'status'::text]))),
    CONSTRAINT space_task_activity_message_check CHECK ((char_length(message) <= 12000)),
    CONSTRAINT space_task_activity_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text))
);

ALTER TABLE ONLY public.space_task_activity FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_task_counters (
    space_id text NOT NULL,
    last_number bigint DEFAULT 0 NOT NULL,
    CONSTRAINT space_task_counters_last_number_check CHECK ((last_number >= 0))
);

CREATE TABLE public.space_tasks (
    id text NOT NULL,
    space_id text NOT NULL,
    title text NOT NULL,
    notes text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'todo'::text NOT NULL,
    assignee_user_id text,
    due_at timestamp with time zone,
    due_timezone text DEFAULT 'UTC'::text NOT NULL,
    source_refs jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_by_user_id text,
    created_by_agent_id text,
    source_run_id text,
    version bigint DEFAULT 1 NOT NULL,
    completed_at timestamp with time zone,
    archived_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    task_number bigint NOT NULL,
    task_key text NOT NULL,
    priority text DEFAULT 'medium'::text NOT NULL,
    rank bigint NOT NULL,
    schedule jsonb,
    calendar jsonb,
    conflicted_fields text[] DEFAULT '{}'::text[] NOT NULL,
    assignee_agent_id text,
    audience_kind text DEFAULT 'space'::text NOT NULL,
    audience_conversation_id text,
    audience_creator_user_id text,
    CONSTRAINT space_tasks_audience_check CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL) AND (audience_creator_user_id IS NOT NULL)))),
    CONSTRAINT space_tasks_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
    CONSTRAINT space_tasks_calendar_check CHECK (((calendar IS NULL) OR (jsonb_typeof(calendar) = 'object'::text))),
    CONSTRAINT space_tasks_check CHECK (((created_by_user_id IS NOT NULL) OR (created_by_agent_id IS NOT NULL))),
    CONSTRAINT space_tasks_due_timezone_check CHECK (((char_length(due_timezone) >= 1) AND (char_length(due_timezone) <= 80))),
    CONSTRAINT space_tasks_notes_check CHECK ((char_length(notes) <= 20000)),
    CONSTRAINT space_tasks_priority_check CHECK ((priority = ANY (ARRAY['high'::text, 'medium'::text, 'low'::text]))),
    CONSTRAINT space_tasks_rank_check CHECK ((rank > 0)),
    CONSTRAINT space_tasks_schedule_check CHECK (((schedule IS NULL) OR (jsonb_typeof(schedule) = 'object'::text))),
    CONSTRAINT space_tasks_single_assignee_check CHECK (((assignee_user_id IS NULL) OR (assignee_agent_id IS NULL))),
    CONSTRAINT space_tasks_source_refs_check CHECK ((jsonb_typeof(source_refs) = 'array'::text)),
    CONSTRAINT space_tasks_status_check CHECK ((status = ANY (ARRAY['todo'::text, 'in_progress'::text, 'done'::text, 'canceled'::text]))),
    CONSTRAINT space_tasks_title_check CHECK (((char_length(title) >= 1) AND (char_length(title) <= 240)))
);

ALTER TABLE ONLY public.space_tasks FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_upload_reservations (
    upload_id text NOT NULL,
    space_id text NOT NULL,
    user_id text NOT NULL,
    reserved_bytes bigint NOT NULL,
    state text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_upload_reservations_reserved_bytes_check CHECK ((reserved_bytes > 0)),
    CONSTRAINT space_upload_reservations_state_check CHECK ((state = ANY (ARRAY['active'::text, 'consumed'::text, 'released'::text])))
);

ALTER TABLE ONLY public.space_upload_reservations FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_workflow_action_journal (
    idempotency_key text NOT NULL,
    run_id text NOT NULL,
    node_id text NOT NULL,
    provider text NOT NULL,
    risk text NOT NULL,
    state text NOT NULL,
    request jsonb DEFAULT '{}'::jsonb NOT NULL,
    result jsonb DEFAULT '{}'::jsonb NOT NULL,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_workflow_action_journal_risk_check CHECK ((risk = ANY (ARRAY['read'::text, 'write'::text, 'destructive'::text]))),
    CONSTRAINT space_workflow_action_journal_state_check CHECK ((state = ANY (ARRAY['started'::text, 'completed'::text, 'failed'::text, 'unknown'::text])))
);

ALTER TABLE ONLY public.space_workflow_action_journal FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_workflow_resource_leases (
    resource_key text NOT NULL,
    run_id text NOT NULL,
    node_id text NOT NULL,
    fingerprint text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.space_workflow_resource_leases FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_workflow_versions (
    id text NOT NULL,
    workflow_id text NOT NULL,
    space_id text NOT NULL,
    stable_identifier text NOT NULL,
    version text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    author_name text DEFAULT ''::text NOT NULL,
    metadata jsonb NOT NULL,
    definition jsonb NOT NULL,
    checksum_sha256 text NOT NULL,
    created_by_user_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT space_workflow_versions_checksum_sha256_check CHECK ((checksum_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT space_workflow_versions_definition_check CHECK ((jsonb_typeof(definition) = 'object'::text)),
    CONSTRAINT space_workflow_versions_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT space_workflow_versions_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 160)))
);

ALTER TABLE ONLY public.space_workflow_versions FORCE ROW LEVEL SECURITY;

CREATE TABLE public.space_workflows (
    id text NOT NULL,
    space_id text NOT NULL,
    creator_user_id text NOT NULL,
    name text NOT NULL,
    definition jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    schedules_enabled boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    stable_identifier text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    author_name text DEFAULT ''::text NOT NULL,
    tags jsonb DEFAULT '[]'::jsonb NOT NULL,
    suggested_agent_preset jsonb DEFAULT '{}'::jsonb NOT NULL,
    source_kind text DEFAULT 'custom'::text NOT NULL,
    forked_from_identifier text,
    CONSTRAINT space_workflows_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80))),
    CONSTRAINT space_workflows_source_kind_check CHECK ((source_kind = ANY (ARRAY['custom'::text, 'installed'::text, 'forked'::text, 'legacy'::text]))),
    CONSTRAINT space_workflows_suggested_agent_preset_check CHECK ((jsonb_typeof(suggested_agent_preset) = 'object'::text)),
    CONSTRAINT space_workflows_tags_check CHECK ((jsonb_typeof(tags) = 'array'::text))
);

ALTER TABLE ONLY public.space_workflows FORCE ROW LEVEL SECURITY;

CREATE TABLE public.spaces (
    id text NOT NULL,
    owner_user_id text NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    security_domain_id text NOT NULL,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    deletion_requested_at timestamp with time zone,
    permanent_delete_after timestamp with time zone,
    is_default boolean DEFAULT false NOT NULL,
    CONSTRAINT spaces_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'pending_deletion'::text, 'deleted'::text]))),
    CONSTRAINT spaces_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80)))
);

ALTER TABLE ONLY public.spaces FORCE ROW LEVEL SECURITY;

CREATE TABLE public.trusted_device_request_nonces (
    device_id text NOT NULL,
    owner_user_id text NOT NULL,
    nonce text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT trusted_device_request_nonces_nonce_check CHECK (((char_length(nonce) >= 16) AND (char_length(nonce) <= 200)))
);

ALTER TABLE ONLY public.trusted_device_request_nonces FORCE ROW LEVEL SECURITY;

CREATE TABLE public.trusted_devices (
    id text NOT NULL,
    user_id text NOT NULL,
    name text NOT NULL,
    public_key text NOT NULL,
    key_algorithm text DEFAULT 'ed25519'::text NOT NULL,
    capabilities jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    platform text DEFAULT 'unknown'::text NOT NULL,
    p2p_endpoint_id text,
    device_protocol_versions jsonb DEFAULT '[]'::jsonb NOT NULL,
    CONSTRAINT trusted_devices_capabilities_check CHECK ((jsonb_typeof(capabilities) = 'object'::text)),
    CONSTRAINT trusted_devices_device_protocol_versions_check CHECK ((jsonb_typeof(device_protocol_versions) = 'array'::text)),
    CONSTRAINT trusted_devices_id_check CHECK ((id ~ '^device_[0-9a-f-]{36}$'::text)),
    CONSTRAINT trusted_devices_key_algorithm_check CHECK ((key_algorithm = 'ed25519'::text)),
    CONSTRAINT trusted_devices_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 100))),
    CONSTRAINT trusted_devices_p2p_endpoint_id_check CHECK (((p2p_endpoint_id IS NULL) OR (p2p_endpoint_id ~ '^[A-Za-z0-9_-]{32,128}$'::text))),
    CONSTRAINT trusted_devices_platform_check CHECK ((platform = ANY (ARRAY['macos'::text, 'windows'::text, 'linux'::text, 'ios'::text, 'android'::text, 'unknown'::text]))),
    CONSTRAINT trusted_devices_public_key_check CHECK (((char_length(public_key) >= 32) AND (char_length(public_key) <= 4096)))
);

ALTER TABLE ONLY public.trusted_devices FORCE ROW LEVEL SECURITY;

CREATE TABLE public.user_app_activity (
    user_id text NOT NULL,
    app_id text NOT NULL,
    open_count bigint DEFAULT 1 NOT NULL,
    last_opened_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_app_activity_app_id_check CHECK (((char_length(app_id) >= 1) AND (char_length(app_id) <= 80))),
    CONSTRAINT user_app_activity_open_count_check CHECK ((open_count > 0))
);

ALTER TABLE ONLY public.user_app_activity FORCE ROW LEVEL SECURITY;

CREATE TABLE public.user_global_home_activity (
    user_id text NOT NULL,
    activity_date date NOT NULL,
    visit_count integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_global_home_activity_visit_count_check CHECK (((visit_count >= 1) AND (visit_count <= 1000000)))
);

ALTER TABLE ONLY public.user_global_home_activity FORCE ROW LEVEL SECURITY;

CREATE TABLE public.user_home_activity (
    user_id text NOT NULL,
    space_id text NOT NULL,
    activity_date date NOT NULL,
    visit_count integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_home_activity_visit_count_check CHECK (((visit_count >= 1) AND (visit_count <= 1000000)))
);

ALTER TABLE ONLY public.user_home_activity FORCE ROW LEVEL SECURITY;

CREATE TABLE public.users (
    id text NOT NULL,
    email text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    license_id text NOT NULL,
    email_updates_enabled boolean DEFAULT false NOT NULL,
    analytics_enabled boolean DEFAULT false NOT NULL,
    error_reporting_enabled boolean DEFAULT false NOT NULL,
    username text NOT NULL,
    avatar_version bigint DEFAULT 0 NOT NULL,
    avatar_updated_at timestamp with time zone,
    lifecycle_state text DEFAULT 'active'::text NOT NULL,
    deletion_requested_at timestamp with time zone,
    anonymized_at timestamp with time zone,
    avatar_object_key text,
    CONSTRAINT users_avatar_object_key_check CHECK (((avatar_object_key IS NULL) OR (avatar_object_key ~ '^avatars/avatar_[0-9a-f-]{36}$'::text))),
    CONSTRAINT users_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['active'::text, 'pending_deletion'::text, 'deleted'::text]))),
    CONSTRAINT users_username_format_check CHECK ((username ~ '^[a-z0-9_]{3,30}$'::text))
);

ALTER TABLE ONLY public.users FORCE ROW LEVEL SECURITY;

CREATE TABLE public.workflow_device_node_jobs (
    id text NOT NULL,
    run_id text,
    node_id text NOT NULL,
    attempt integer NOT NULL,
    user_id text NOT NULL,
    scope_id text NOT NULL,
    operation text NOT NULL,
    input jsonb NOT NULL,
    config jsonb NOT NULL,
    input_schema jsonb DEFAULT '{}'::jsonb NOT NULL,
    output_schema jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'queued'::text NOT NULL,
    leased_device_id text,
    lease_token_hash text,
    lease_expires_at timestamp with time zone,
    last_heartbeat_at timestamp with time zone,
    output jsonb,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    assigned_device_id text,
    context_id text,
    invocation_id text,
    ai_context_id text,
    control_version integer DEFAULT 2 NOT NULL,
    deadline_at timestamp with time zone NOT NULL,
    runtime_run_id text DEFAULT ''::text NOT NULL,
    required_capability text DEFAULT ''::text NOT NULL,
    execution_started_at timestamp with time zone,
    cancel_requested_at timestamp with time zone,
    delivery_attempts integer DEFAULT 0 NOT NULL,
    recovery_attempts integer DEFAULT 0 NOT NULL,
    CONSTRAINT workflow_device_node_jobs_attempt_check CHECK (((attempt >= 1) AND (attempt <= 3))),
    CONSTRAINT workflow_device_node_jobs_control_version_check CHECK ((control_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT workflow_device_node_jobs_owner_check CHECK (((((run_id IS NOT NULL))::integer + ((invocation_id IS NOT NULL))::integer) = 1)),
    CONSTRAINT workflow_device_node_jobs_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'leased'::text, 'executing'::text, 'completed'::text, 'failed'::text, 'canceled'::text, 'uncertain'::text])))
);

ALTER TABLE ONLY public.workflow_device_node_jobs FORCE ROW LEVEL SECURITY;

ALTER TABLE ONLY public.mail_action_audit ALTER COLUMN id SET DEFAULT nextval('public.mail_action_audit_id_seq'::regclass);

ALTER TABLE ONLY public.misty_ask_conversation_events ALTER COLUMN id SET DEFAULT nextval('public.agent_conversation_events_id_seq'::regclass);

ALTER TABLE ONLY public.provider_event_inbox ALTER COLUMN id SET DEFAULT nextval('public.provider_event_inbox_id_seq'::regclass);

ALTER TABLE ONLY public.smart_library_cost_events ALTER COLUMN id SET DEFAULT nextval('public.smart_library_cost_events_id_seq'::regclass);

ALTER TABLE ONLY public.space_events ALTER COLUMN id SET DEFAULT nextval('public.space_events_id_seq'::regclass);

ALTER TABLE ONLY public.space_inbox_items ALTER COLUMN id SET DEFAULT nextval('public.space_inbox_items_id_seq'::regclass);

ALTER TABLE ONLY public.space_library_audit_events ALTER COLUMN id SET DEFAULT nextval('public.space_library_audit_events_id_seq'::regclass);

ALTER TABLE ONLY public.space_messages ALTER COLUMN seq SET DEFAULT nextval('public.space_messages_seq_seq'::regclass);

ALTER TABLE ONLY public.abuse_blocks
    ADD CONSTRAINT abuse_blocks_pkey PRIMARY KEY (block_key);

ALTER TABLE ONLY public.account_deletion_provider_resources
    ADD CONSTRAINT account_deletion_provider_resources_pkey PRIMARY KEY (request_id, kind, resource_id);

ALTER TABLE ONLY public.account_deletion_requests
    ADD CONSTRAINT account_deletion_requests_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.account_deletion_requests
    ADD CONSTRAINT account_deletion_requests_status_token_hash_key UNIQUE (status_token_hash);

ALTER TABLE ONLY public.account_deletion_steps
    ADD CONSTRAINT account_deletion_steps_pkey PRIMARY KEY (request_id, step);

ALTER TABLE ONLY public.misty_ask_conversation_events
    ADD CONSTRAINT agent_conversation_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.misty_ask_conversations
    ADD CONSTRAINT agent_conversations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.agent_model_turn_claims
    ADD CONSTRAINT agent_model_turn_claims_pkey PRIMARY KEY (run_id, node_id);

ALTER TABLE ONLY public.agent_run_contexts
    ADD CONSTRAINT agent_run_contexts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.agent_run_contexts
    ADD CONSTRAINT agent_run_contexts_run_id_kind_opaque_ref_key UNIQUE (run_id, kind, opaque_ref);

ALTER TABLE ONLY public.agent_run_tool_approvals
    ADD CONSTRAINT agent_run_tool_approvals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.agent_run_tool_approvals
    ADD CONSTRAINT agent_run_tool_approvals_run_id_tool_call_id_key UNIQUE (run_id, tool_call_id);

ALTER TABLE ONLY public.agent_runtime_deliveries
    ADD CONSTRAINT agent_runtime_deliveries_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.agent_runtime_start_receipts
    ADD CONSTRAINT agent_runtime_start_receipts_pkey PRIMARY KEY (run_id);

ALTER TABLE ONLY public.agent_sdk_capability_bindings
    ADD CONSTRAINT agent_sdk_capability_bindings_pkey PRIMARY KEY (run_id, target_id, capability);

ALTER TABLE ONLY public.agent_toolbox_action_journal
    ADD CONSTRAINT agent_toolbox_action_journal_pkey PRIMARY KEY (idempotency_key);

ALTER TABLE ONLY public.ai_artifacts
    ADD CONSTRAINT ai_artifacts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_artifacts
    ADD CONSTRAINT ai_artifacts_user_id_idempotency_key_key UNIQUE (user_id, idempotency_key);

ALTER TABLE ONLY public.ai_cleanup_jobs
    ADD CONSTRAINT ai_cleanup_jobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_conversation_attachments
    ADD CONSTRAINT ai_conversation_attachments_model_object_key_key UNIQUE (model_object_key);

ALTER TABLE ONLY public.ai_conversation_attachments
    ADD CONSTRAINT ai_conversation_attachments_object_key_key UNIQUE (object_key);

ALTER TABLE ONLY public.ai_conversation_attachments
    ADD CONSTRAINT ai_conversation_attachments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_feature_flags
    ADD CONSTRAINT ai_feature_flags_pkey PRIMARY KEY (surface_id, action_id, model_id);

ALTER TABLE ONLY public.ai_feedback
    ADD CONSTRAINT ai_feedback_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_feedback
    ADD CONSTRAINT ai_feedback_user_id_invocation_id_key UNIQUE (user_id, invocation_id);

ALTER TABLE ONLY public.ai_intervention_waits
    ADD CONSTRAINT ai_intervention_waits_invocation_id_call_id_key UNIQUE (invocation_id, call_id);

ALTER TABLE ONLY public.ai_intervention_waits
    ADD CONSTRAINT ai_intervention_waits_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_invocation_id_kind_opaque_ref_key UNIQUE (invocation_id, kind, opaque_ref);

ALTER TABLE ONLY public.ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_invocation_events
    ADD CONSTRAINT ai_invocation_events_pkey PRIMARY KEY (invocation_id, sequence);

ALTER TABLE ONLY public.ai_invocations
    ADD CONSTRAINT ai_invocations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_invocations
    ADD CONSTRAINT ai_invocations_user_id_idempotency_key_key UNIQUE (user_id, idempotency_key);

ALTER TABLE ONLY public.ai_recaps
    ADD CONSTRAINT ai_recaps_pkey PRIMARY KEY (user_id, surface_id);

ALTER TABLE ONLY public.ai_retrieval_chunks
    ADD CONSTRAINT ai_retrieval_chunks_pkey PRIMARY KEY (document_id, ordinal);

ALTER TABLE ONLY public.ai_retrieval_documents
    ADD CONSTRAINT ai_retrieval_documents_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ai_retrieval_documents
    ADD CONSTRAINT ai_retrieval_documents_source_kind_source_id_key UNIQUE (source_kind, source_id);

ALTER TABLE ONLY public.ai_runtime_callback_receipts
    ADD CONSTRAINT ai_runtime_callback_receipts_pkey PRIMARY KEY (invocation_id, effect_key);

ALTER TABLE ONLY public.ai_surface_preferences
    ADD CONSTRAINT ai_surface_preferences_pkey PRIMARY KEY (user_id, surface_id);

ALTER TABLE ONLY public.ai_user_settings
    ADD CONSTRAINT ai_user_settings_pkey PRIMARY KEY (user_id);

ALTER TABLE ONLY public.auth_handoff_tokens
    ADD CONSTRAINT auth_handoff_tokens_pkey PRIMARY KEY (hashed_token);

ALTER TABLE ONLY public.billing_adapter_intents
    ADD CONSTRAINT billing_adapter_intents_pkey PRIMARY KEY (account_id, key);

ALTER TABLE ONLY public.billing_adapter_outbox
    ADD CONSTRAINT billing_adapter_outbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.billing_adapter_reservations
    ADD CONSTRAINT billing_adapter_reservations_pkey PRIMARY KEY (account_id, key);

ALTER TABLE ONLY public.browser_sync_checkpoints
    ADD CONSTRAINT browser_sync_checkpoints_pkey PRIMARY KEY (workspace_id, sequence);

ALTER TABLE ONLY public.browser_sync_connections
    ADD CONSTRAINT browser_sync_connections_pkey PRIMARY KEY (connection_id);

ALTER TABLE ONLY public.browser_sync_control_requests
    ADD CONSTRAINT browser_sync_control_requests_pkey PRIMARY KEY (workspace_id, operation_id);

ALTER TABLE ONLY public.browser_sync_devices
    ADD CONSTRAINT browser_sync_devices_pkey PRIMARY KEY (workspace_id, device_id);

ALTER TABLE ONLY public.browser_sync_events
    ADD CONSTRAINT browser_sync_events_pkey PRIMARY KEY (workspace_id, sequence);

ALTER TABLE ONLY public.browser_sync_events
    ADD CONSTRAINT browser_sync_events_workspace_id_device_id_device_counter_key UNIQUE (workspace_id, device_id, device_counter);

ALTER TABLE ONLY public.browser_sync_events
    ADD CONSTRAINT browser_sync_events_workspace_id_operation_id_key UNIQUE (workspace_id, operation_id);

ALTER TABLE ONLY public.browser_sync_receipts
    ADD CONSTRAINT browser_sync_receipts_pkey PRIMARY KEY (workspace_id, operation_id);

ALTER TABLE ONLY public.browser_sync_receipts
    ADD CONSTRAINT browser_sync_receipts_workspace_id_device_id_device_counter_key UNIQUE (workspace_id, device_id, device_counter);

ALTER TABLE ONLY public.browser_sync_tickets
    ADD CONSTRAINT browser_sync_tickets_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY public.browser_sync_workspaces
    ADD CONSTRAINT browser_sync_workspaces_pkey PRIMARY KEY (user_id);

ALTER TABLE ONLY public.browser_sync_workspaces
    ADD CONSTRAINT browser_sync_workspaces_workspace_id_key UNIQUE (workspace_id);

ALTER TABLE ONLY public.cloud_connections
    ADD CONSTRAINT cloud_connections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.cloud_connections
    ADD CONSTRAINT cloud_connections_user_id_name_key UNIQUE (user_id, name);

ALTER TABLE ONLY public.cloud_connections
    ADD CONSTRAINT cloud_connections_user_id_provider_account_id_key UNIQUE (user_id, provider, account_id);

ALTER TABLE ONLY public.cloud_credential_handoffs
    ADD CONSTRAINT cloud_credential_handoffs_pkey PRIMARY KEY (handoff_hash);

ALTER TABLE ONLY public.cloud_oauth_states
    ADD CONSTRAINT cloud_oauth_states_pkey PRIMARY KEY (state_hash);

ALTER TABLE ONLY public.connected_account_oauth_states
    ADD CONSTRAINT connected_account_oauth_states_pkey PRIMARY KEY (state_hash);

ALTER TABLE ONLY public.connected_accounts
    ADD CONSTRAINT connected_accounts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.connected_accounts
    ADD CONSTRAINT connected_accounts_user_id_provider_account_id_key UNIQUE (user_id, provider, account_id);

ALTER TABLE ONLY public.connection_authorization_requests
    ADD CONSTRAINT connection_authorization_requests_pkey PRIMARY KEY (state_hash);

ALTER TABLE ONLY public.device_pairing_sessions
    ADD CONSTRAINT device_pairing_sessions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.device_pairs
    ADD CONSTRAINT device_pairs_owner_user_id_first_device_id_second_device_id_key UNIQUE (owner_user_id, first_device_id, second_device_id);

ALTER TABLE ONLY public.device_pairs
    ADD CONSTRAINT device_pairs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.device_presence
    ADD CONSTRAINT device_presence_pkey PRIMARY KEY (device_id);

ALTER TABLE ONLY public.figma_comment_audit
    ADD CONSTRAINT figma_comment_audit_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.figma_content_records
    ADD CONSTRAINT figma_content_records_binding_id_record_type_external_id_key UNIQUE (binding_id, record_type, external_id);

ALTER TABLE ONLY public.figma_content_records
    ADD CONSTRAINT figma_content_records_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_shared_resource_id_key UNIQUE (shared_resource_id);

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_space_id_resource_type_external_id_key UNIQUE (space_id, resource_type, external_id);

ALTER TABLE ONLY public.figma_webhook_deliveries
    ADD CONSTRAINT figma_webhook_deliveries_pkey PRIMARY KEY (delivery_hash);

ALTER TABLE ONLY public.figma_webhook_subscriptions
    ADD CONSTRAINT figma_webhook_subscriptions_binding_id_event_type_key UNIQUE (binding_id, event_type);

ALTER TABLE ONLY public.figma_webhook_subscriptions
    ADD CONSTRAINT figma_webhook_subscriptions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.figma_webhook_subscriptions
    ADD CONSTRAINT figma_webhook_subscriptions_webhook_id_key UNIQUE (webhook_id);

ALTER TABLE ONLY public.github_app_installations
    ADD CONSTRAINT github_app_installations_integration_id_key UNIQUE (integration_id);

ALTER TABLE ONLY public.github_app_installations
    ADD CONSTRAINT github_app_installations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.github_app_installations
    ADD CONSTRAINT github_app_installations_space_id_installation_id_key UNIQUE (space_id, installation_id);

ALTER TABLE ONLY public.github_app_setup_states
    ADD CONSTRAINT github_app_setup_states_pkey PRIMARY KEY (state_hash);

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_shared_resource_id_key UNIQUE (shared_resource_id);

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_space_id_repository_id_key UNIQUE (space_id, repository_id);

ALTER TABLE ONLY public.github_credential_handoffs
    ADD CONSTRAINT github_credential_handoffs_pkey PRIMARY KEY (handle_hash);

ALTER TABLE ONLY public.github_mutation_audit
    ADD CONSTRAINT github_mutation_audit_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.github_repository_records
    ADD CONSTRAINT github_repository_records_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.github_repository_records
    ADD CONSTRAINT github_repository_records_workspace_id_record_type_external_key UNIQUE (workspace_id, record_type, external_id);

ALTER TABLE ONLY public.github_webhook_deliveries
    ADD CONSTRAINT github_webhook_deliveries_pkey PRIMARY KEY (delivery_id);

ALTER TABLE ONLY public.library_blobs
    ADD CONSTRAINT library_blobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_blobs
    ADD CONSTRAINT library_blobs_r2_object_key_key UNIQUE (r2_object_key);

ALTER TABLE ONLY public.library_derivatives
    ADD CONSTRAINT library_derivatives_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_exports
    ADD CONSTRAINT library_exports_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_files
    ADD CONSTRAINT library_files_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_item_versions
    ADD CONSTRAINT library_item_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_item_versions
    ADD CONSTRAINT library_item_versions_space_library_item_id_version_number_key UNIQUE (space_library_item_id, version_number);

ALTER TABLE ONLY public.library_legal_holds
    ADD CONSTRAINT library_legal_holds_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_processing_jobs
    ADD CONSTRAINT library_processing_jobs_job_kind_target_kind_target_id_key UNIQUE (job_kind, target_kind, target_id);

ALTER TABLE ONLY public.library_processing_jobs
    ADD CONSTRAINT library_processing_jobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_reauthentication_grants
    ADD CONSTRAINT library_reauthentication_grants_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_reauthentication_grants
    ADD CONSTRAINT library_reauthentication_grants_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY public.library_recovery_tombstones
    ADD CONSTRAINT library_recovery_tombstones_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_recovery_tombstones
    ADD CONSTRAINT library_recovery_tombstones_target_kind_target_id_key UNIQUE (target_kind, target_id);

ALTER TABLE ONLY public.licenses
    ADD CONSTRAINT licenses_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.licenses
    ADD CONSTRAINT licenses_user_id_key UNIQUE (user_id);

ALTER TABLE ONLY public.mail_action_audit
    ADD CONSTRAINT mail_action_audit_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.mcp_discovery_snapshots
    ADD CONSTRAINT mcp_discovery_snapshots_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.mcp_oauth_credentials
    ADD CONSTRAINT mcp_oauth_credentials_pkey PRIMARY KEY (connection_id);

ALTER TABLE ONLY public.mcp_oauth_states
    ADD CONSTRAINT mcp_oauth_states_pkey PRIMARY KEY (state_hash);

ALTER TABLE ONLY public.mcp_remote_connections
    ADD CONSTRAINT mcp_remote_connections_owner_user_id_endpoint_url_key UNIQUE (owner_user_id, endpoint_url);

ALTER TABLE ONLY public.mcp_remote_connections
    ADD CONSTRAINT mcp_remote_connections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.mcp_remote_tools
    ADD CONSTRAINT mcp_remote_tools_connection_id_id_key UNIQUE (connection_id, id);

ALTER TABLE ONLY public.mcp_remote_tools
    ADD CONSTRAINT mcp_remote_tools_connection_id_remote_name_key UNIQUE (connection_id, remote_name);

ALTER TABLE ONLY public.mcp_remote_tools
    ADD CONSTRAINT mcp_remote_tools_connection_id_stable_name_key UNIQUE (connection_id, stable_name);

ALTER TABLE ONLY public.mcp_remote_tools
    ADD CONSTRAINT mcp_remote_tools_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.mcp_tool_execution_audit
    ADD CONSTRAINT mcp_tool_execution_audit_owner_user_id_idempotency_key_key UNIQUE (owner_user_id, idempotency_key);

ALTER TABLE ONLY public.mcp_tool_execution_audit
    ADD CONSTRAINT mcp_tool_execution_audit_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.media_search_assets
    ADD CONSTRAINT media_search_assets_pkey PRIMARY KEY (user_id, device_id, asset_id);

ALTER TABLE ONLY public.media_search_chunks
    ADD CONSTRAINT media_search_chunks_pkey PRIMARY KEY (user_id, device_id, asset_id, chunk_index);

ALTER TABLE ONLY public.media_search_devices
    ADD CONSTRAINT media_search_devices_pkey PRIMARY KEY (user_id, device_id);

ALTER TABLE ONLY public.media_search_segments
    ADD CONSTRAINT media_search_segments_device_asset_kind_time_key UNIQUE (user_id, device_id, asset_id, segment_kind, start_ms, end_ms);

ALTER TABLE ONLY public.media_search_segments
    ADD CONSTRAINT media_search_segments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.misty_agent_execution_leases
    ADD CONSTRAINT misty_agent_execution_leases_pkey PRIMARY KEY (owner_user_id, agent_id);

ALTER TABLE ONLY public.misty_agent_execution_leases
    ADD CONSTRAINT misty_agent_execution_leases_task_id_key UNIQUE (task_id);

ALTER TABLE ONLY public.misty_conversation_focus
    ADD CONSTRAINT misty_conversation_focus_pkey PRIMARY KEY (user_id, conversation_id, space_id, entity_kind);

ALTER TABLE ONLY public.misty_conversation_pending_actions
    ADD CONSTRAINT misty_conversation_pending_actions_pkey PRIMARY KEY (user_id, conversation_id, space_id);

ALTER TABLE ONLY public.misty_instance
    ADD CONSTRAINT misty_instance_pkey PRIMARY KEY (singleton);

ALTER TABLE ONLY public.misty_instance
    ADD CONSTRAINT misty_instance_server_id_key UNIQUE (server_id);

ALTER TABLE ONLY public.misty_memories
    ADD CONSTRAINT misty_memories_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.misty_memories
    ADD CONSTRAINT misty_memories_user_id_scope_key_memory_key_key UNIQUE (user_id, scope_key, memory_key);

ALTER TABLE ONLY public.native_task_effects
    ADD CONSTRAINT native_task_effects_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.native_task_effects
    ADD CONSTRAINT native_task_effects_task_id_task_version_event_kind_key UNIQUE (task_id, task_version, event_kind);

ALTER TABLE ONLY public.object_deletion_jobs
    ADD CONSTRAINT object_deletion_jobs_pkey PRIMARY KEY (object_key);

ALTER TABLE ONLY public.owner_storage_usage
    ADD CONSTRAINT owner_storage_usage_pkey PRIMARY KEY (owner_user_id);

ALTER TABLE ONLY public.password_recovery_jobs
    ADD CONSTRAINT password_recovery_jobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.password_recovery_jobs
    ADD CONSTRAINT password_recovery_jobs_request_order_key UNIQUE (request_order);

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_hashed_token_key UNIQUE (hashed_token);

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_pkey PRIMARY KEY (user_id);

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_agent_id_connection_id_remote_tool_key UNIQUE (agent_id, connection_id, remote_tool_id);

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_agent_id_stable_name_key UNIQUE (agent_id, stable_name);

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.agent_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_pkey PRIMARY KEY (run_id);

ALTER TABLE ONLY public.misty_ask_identity_versions
    ADD CONSTRAINT personal_agent_versions_agent_id_version_key UNIQUE (agent_id, version);

ALTER TABLE ONLY public.misty_ask_identity_versions
    ADD CONSTRAINT personal_agent_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.misty_ask_identities
    ADD CONSTRAINT personal_agents_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.personal_space_templates
    ADD CONSTRAINT personal_space_templates_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_content_records
    ADD CONSTRAINT provider_content_records_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_content_records
    ADD CONSTRAINT provider_content_records_shared_resource_id_external_record_key UNIQUE (shared_resource_id, external_record_id);

ALTER TABLE ONLY public.provider_event_inbox
    ADD CONSTRAINT provider_event_inbox_integration_id_external_event_id_key UNIQUE (integration_id, external_event_id);

ALTER TABLE ONLY public.provider_event_inbox
    ADD CONSTRAINT provider_event_inbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_gateway_state
    ADD CONSTRAINT provider_gateway_state_pkey PRIMARY KEY (provider);

ALTER TABLE ONLY public.provider_oauth_states
    ADD CONSTRAINT provider_oauth_states_pkey PRIMARY KEY (state_hash);

ALTER TABLE ONLY public.provider_shared_resources
    ADD CONSTRAINT provider_shared_resources_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_shared_resources
    ADD CONSTRAINT provider_shared_resources_space_id_integration_id_provider__key UNIQUE (space_id, integration_id, provider, resource_type, external_resource_id);

ALTER TABLE ONLY public.provider_subscriptions
    ADD CONSTRAINT provider_subscriptions_integration_id_resource_key_key UNIQUE (integration_id, resource_key);

ALTER TABLE ONLY public.provider_subscriptions
    ADD CONSTRAINT provider_subscriptions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.realtime_tickets
    ADD CONSTRAINT realtime_tickets_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY public.sdk_backend_connection_versions
    ADD CONSTRAINT sdk_backend_connection_versions_pkey PRIMARY KEY (user_id, id, revision);

ALTER TABLE ONLY public.sdk_backend_connections
    ADD CONSTRAINT sdk_backend_connections_pkey PRIMARY KEY (user_id, id);

ALTER TABLE ONLY public.sdk_capability_contract_versions
    ADD CONSTRAINT sdk_capability_contract_versions_pkey PRIMARY KEY (user_id, name, version);

ALTER TABLE ONLY public.sdk_capability_invocations
    ADD CONSTRAINT sdk_capability_invocations_effect_id_key UNIQUE (effect_id);

ALTER TABLE ONLY public.sdk_capability_invocations
    ADD CONSTRAINT sdk_capability_invocations_invocation_id_key UNIQUE (invocation_id);

ALTER TABLE ONLY public.sdk_capability_invocations
    ADD CONSTRAINT sdk_capability_invocations_pkey PRIMARY KEY (user_id, caller_app_id, request_id);

ALTER TABLE ONLY public.sdk_provider_registrations
    ADD CONSTRAINT sdk_provider_registrations_pkey PRIMARY KEY (user_id, space_id, provider_id);

ALTER TABLE ONLY public.sdk_provider_versions
    ADD CONSTRAINT sdk_provider_versions_pkey PRIMARY KEY (user_id, provider_id, version);

ALTER TABLE ONLY public.sdk_target_versions
    ADD CONSTRAINT sdk_target_versions_pkey PRIMARY KEY (user_id, id, revision);

ALTER TABLE ONLY public.sdk_targets
    ADD CONSTRAINT sdk_targets_pkey PRIMARY KEY (user_id, id);

ALTER TABLE ONLY public.security_domains
    ADD CONSTRAINT security_domains_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.self_host_accounts
    ADD CONSTRAINT self_host_accounts_entitlement_subject_key UNIQUE (entitlement_subject);

ALTER TABLE ONLY public.self_host_accounts
    ADD CONSTRAINT self_host_accounts_pkey PRIMARY KEY (user_id);

ALTER TABLE ONLY public.self_host_bootstrap_tokens
    ADD CONSTRAINT self_host_bootstrap_tokens_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY public.self_host_collaboration_documents
    ADD CONSTRAINT self_host_collaboration_documents_pkey PRIMARY KEY (resource_type, resource_id);

ALTER TABLE ONLY public.self_host_enrollment_invitations
    ADD CONSTRAINT self_host_enrollment_invitations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.self_host_enrollment_invitations
    ADD CONSTRAINT self_host_enrollment_invitations_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY public.smart_library_assets
    ADD CONSTRAINT smart_library_assets_pkey PRIMARY KEY (folder_id, asset_id);

ALTER TABLE ONLY public.smart_library_assets
    ADD CONSTRAINT smart_library_assets_result_sequence_key UNIQUE (result_sequence);

ALTER TABLE ONLY public.smart_library_batches
    ADD CONSTRAINT smart_library_batches_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.smart_library_cost_events
    ADD CONSTRAINT smart_library_cost_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.smart_library_folders
    ADD CONSTRAINT smart_library_folders_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.smart_library_reindex_jobs
    ADD CONSTRAINT smart_library_reindex_jobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_automation_rules
    ADD CONSTRAINT social_automation_rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_automation_runs
    ADD CONSTRAINT social_automation_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_bindings
    ADD CONSTRAINT social_bindings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_bindings
    ADD CONSTRAINT social_bindings_space_id_provider_external_resource_id_key UNIQUE (space_id, provider, external_resource_id);

ALTER TABLE ONLY public.social_identities
    ADD CONSTRAINT social_identities_binding_id_external_user_id_key UNIQUE (binding_id, external_user_id);

ALTER TABLE ONLY public.social_identities
    ADD CONSTRAINT social_identities_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_idempotency_key_key UNIQUE (idempotency_key);

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.social_send_authorities
    ADD CONSTRAINT social_send_authorities_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_album_folders
    ADD CONSTRAINT space_album_folders_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_album_items
    ADD CONSTRAINT space_album_items_pkey PRIMARY KEY (album_id, space_library_item_id);

ALTER TABLE ONLY public.space_albums
    ADD CONSTRAINT space_albums_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_albums
    ADD CONSTRAINT space_albums_space_id_name_key UNIQUE (space_id, name);

ALTER TABLE ONLY public.space_calendar_events
    ADD CONSTRAINT space_calendar_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_calendar_events
    ADD CONSTRAINT space_calendar_events_source_id_external_event_id_key UNIQUE (source_id, external_event_id);

ALTER TABLE ONLY public.space_calendar_sources
    ADD CONSTRAINT space_calendar_sources_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_calendar_sources
    ADD CONSTRAINT space_calendar_sources_space_id_integration_id_external_cal_key UNIQUE (space_id, integration_id, external_calendar_id);

ALTER TABLE ONLY public.space_conversation_reads
    ADD CONSTRAINT space_conversation_reads_pkey PRIMARY KEY (conversation_id, user_id);

ALTER TABLE ONLY public.space_conversations
    ADD CONSTRAINT space_conversations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_creation_requests
    ADD CONSTRAINT space_creation_requests_pkey PRIMARY KEY (user_id, idempotency_key);

ALTER TABLE ONLY public.space_device_presence
    ADD CONSTRAINT space_device_presence_owner_user_id_device_id_endpoint_id_key UNIQUE (owner_user_id, device_id, endpoint_id);

ALTER TABLE ONLY public.space_device_presence
    ADD CONSTRAINT space_device_presence_pkey PRIMARY KEY (owner_user_id, device_id, space_id, app_id);

ALTER TABLE ONLY public.space_discord_links
    ADD CONSTRAINT space_discord_links_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_discord_links
    ADD CONSTRAINT space_discord_links_space_id_channel_id_key UNIQUE (space_id, channel_id);

ALTER TABLE ONLY public.space_drawing_assets
    ADD CONSTRAINT space_drawing_assets_drawing_id_excalidraw_file_id_key UNIQUE (drawing_id, excalidraw_file_id);

ALTER TABLE ONLY public.space_drawing_assets
    ADD CONSTRAINT space_drawing_assets_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_drawing_control_outbox
    ADD CONSTRAINT space_drawing_control_outbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_drawings
    ADD CONSTRAINT space_drawings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_events
    ADD CONSTRAINT space_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_inbox_items
    ADD CONSTRAINT space_inbox_items_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_integrations
    ADD CONSTRAINT space_integrations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_integrations
    ADD CONSTRAINT space_integrations_private_account_key UNIQUE (space_id, connected_by_user_id, provider, display_name);

ALTER TABLE ONLY public.space_invitation_delivery_jobs
    ADD CONSTRAINT space_invitation_delivery_jobs_pkey PRIMARY KEY (invite_id);

ALTER TABLE ONLY public.space_invitations
    ADD CONSTRAINT space_invitations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_item_aliases
    ADD CONSTRAINT space_item_aliases_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_item_aliases
    ADD CONSTRAINT space_item_aliases_space_id_normalized_alias_key UNIQUE (space_id, normalized_alias);

ALTER TABLE ONLY public.space_item_aliases
    ADD CONSTRAINT space_item_aliases_space_id_target_kind_target_id_key UNIQUE (space_id, target_kind, target_id);

ALTER TABLE ONLY public.space_library_asset_stack_members
    ADD CONSTRAINT space_library_asset_stack_members_pkey PRIMARY KEY (stack_id, space_library_item_id);

ALTER TABLE ONLY public.space_library_asset_stack_members
    ADD CONSTRAINT space_library_asset_stack_members_stack_id_position_key UNIQUE (stack_id, "position");

ALTER TABLE ONLY public.space_library_asset_stacks
    ADD CONSTRAINT space_library_asset_stacks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_audit_events
    ADD CONSTRAINT space_library_audit_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_direct_references
    ADD CONSTRAINT space_library_direct_referenc_destination_space_id_grant_id_key UNIQUE (destination_space_id, grant_id);

ALTER TABLE ONLY public.space_library_direct_references
    ADD CONSTRAINT space_library_direct_references_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_grants
    ADD CONSTRAINT space_library_grants_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_groups
    ADD CONSTRAINT space_library_groups_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_groups
    ADD CONSTRAINT space_library_groups_space_id_name_key UNIQUE (space_id, name);

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_intelligence_policies
    ADD CONSTRAINT space_library_intelligence_policies_pkey PRIMARY KEY (space_id);

ALTER TABLE ONLY public.space_library_item_views
    ADD CONSTRAINT space_library_item_views_pkey PRIMARY KEY (space_id, space_library_item_id, user_id);

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_library_search_documents
    ADD CONSTRAINT space_library_search_documents_pkey PRIMARY KEY (space_id, space_library_item_id);

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_object_key_key UNIQUE (object_key);

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_member_permission_overrides
    ADD CONSTRAINT space_member_permission_overrides_pkey PRIMARY KEY (space_id, user_id, permission);

ALTER TABLE ONLY public.space_member_roles
    ADD CONSTRAINT space_member_roles_pkey PRIMARY KEY (space_id, user_id, role_id);

ALTER TABLE ONLY public.space_member_storage_usage
    ADD CONSTRAINT space_member_storage_usage_pkey PRIMARY KEY (space_id, user_id);

ALTER TABLE ONLY public.space_members
    ADD CONSTRAINT space_members_pkey PRIMARY KEY (space_id, user_id);

ALTER TABLE ONLY public.space_memory_preferences
    ADD CONSTRAINT space_memory_preferences_pkey PRIMARY KEY (space_id, memory_id);

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_upload_id_key UNIQUE (upload_id);

ALTER TABLE ONLY public.space_message_library_references
    ADD CONSTRAINT space_message_library_references_pkey PRIMARY KEY (message_id, space_library_item_id);

ALTER TABLE ONLY public.space_message_reactions
    ADD CONSTRAINT space_message_reactions_pkey PRIMARY KEY (message_id, user_id, emoji);

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_seq_key UNIQUE (seq);

ALTER TABLE ONLY public.space_native_calendar_events
    ADD CONSTRAINT space_native_calendar_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_nodes
    ADD CONSTRAINT space_nodes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_note_assets
    ADD CONSTRAINT space_note_assets_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_note_control_outbox
    ADD CONSTRAINT space_note_control_outbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_note_links
    ADD CONSTRAINT space_note_links_pkey PRIMARY KEY (source_note_id, target_note_id);

ALTER TABLE ONLY public.space_note_permissions
    ADD CONSTRAINT space_note_permissions_pkey PRIMARY KEY (note_id, user_id);

ALTER TABLE ONLY public.space_notes
    ADD CONSTRAINT space_notes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_people
    ADD CONSTRAINT space_people_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_person_observations
    ADD CONSTRAINT space_person_observations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_pinned_collections
    ADD CONSTRAINT space_pinned_collections_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_pinned_collections
    ADD CONSTRAINT space_pinned_collections_space_id_position_key UNIQUE (space_id, "position");

ALTER TABLE ONLY public.space_pinned_collections
    ADD CONSTRAINT space_pinned_collections_space_id_target_kind_target_id_key UNIQUE (space_id, target_kind, target_id);

ALTER TABLE ONLY public.space_provider_credentials
    ADD CONSTRAINT space_provider_credentials_integration_id_key UNIQUE (integration_id);

ALTER TABLE ONLY public.space_provider_credentials
    ADD CONSTRAINT space_provider_credentials_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_provider_credentials
    ADD CONSTRAINT space_provider_credentials_space_id_user_id_provider_accoun_key UNIQUE (space_id, user_id, provider, account_id);

ALTER TABLE ONLY public.space_rendition_reservations
    ADD CONSTRAINT space_rendition_reservations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_rendition_reservations
    ADD CONSTRAINT space_rendition_reservations_source_kind_source_id_key UNIQUE (source_kind, source_id);

ALTER TABLE ONLY public.space_resolve_tickets
    ADD CONSTRAINT space_resolve_tickets_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY public.space_roadmap_edges
    ADD CONSTRAINT space_roadmap_edges_id_roadmap_id_space_id_key UNIQUE (id, roadmap_id, space_id);

ALTER TABLE ONLY public.space_roadmap_edges
    ADD CONSTRAINT space_roadmap_edges_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_roadmap_edges
    ADD CONSTRAINT space_roadmap_edges_roadmap_id_source_goal_id_target_goal_i_key UNIQUE (roadmap_id, source_goal_id, target_goal_id, edge_type);

ALTER TABLE ONLY public.space_roadmap_goal_tasks
    ADD CONSTRAINT space_roadmap_goal_tasks_pkey PRIMARY KEY (goal_id, task_id);

ALTER TABLE ONLY public.space_roadmap_goals
    ADD CONSTRAINT space_roadmap_goals_id_roadmap_id_space_id_key UNIQUE (id, roadmap_id, space_id);

ALTER TABLE ONLY public.space_roadmap_goals
    ADD CONSTRAINT space_roadmap_goals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_roadmap_milestones
    ADD CONSTRAINT space_roadmap_milestones_id_roadmap_id_space_id_key UNIQUE (id, roadmap_id, space_id);

ALTER TABLE ONLY public.space_roadmap_milestones
    ADD CONSTRAINT space_roadmap_milestones_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_roadmap_node_definitions
    ADD CONSTRAINT space_roadmap_node_definitions_id_space_id_key UNIQUE (id, space_id);

ALTER TABLE ONLY public.space_roadmap_node_definitions
    ADD CONSTRAINT space_roadmap_node_definitions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_roadmap_nodes
    ADD CONSTRAINT space_roadmap_nodes_id_roadmap_id_space_id_key UNIQUE (id, roadmap_id, space_id);

ALTER TABLE ONLY public.space_roadmap_nodes
    ADD CONSTRAINT space_roadmap_nodes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_roadmaps
    ADD CONSTRAINT space_roadmaps_id_space_id_key UNIQUE (id, space_id);

ALTER TABLE ONLY public.space_roadmaps
    ADD CONSTRAINT space_roadmaps_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_roles
    ADD CONSTRAINT space_roles_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_run_actions
    ADD CONSTRAINT space_run_actions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_run_approvals
    ADD CONSTRAINT space_run_approvals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_run_steps
    ADD CONSTRAINT space_run_steps_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_run_steps
    ADD CONSTRAINT space_run_steps_run_id_node_id_key UNIQUE (run_id, node_id);

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_setup_integrations
    ADD CONSTRAINT space_setup_integrations_pkey PRIMARY KEY (space_id, provider);

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_shared_resource_id_key UNIQUE (shared_resource_id);

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_space_id_channel_id_key UNIQUE (space_id, channel_id);

ALTER TABLE ONLY public.space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_space_id_user_id_source_kind_so_key UNIQUE (space_id, user_id, source_kind, source_id);

ALTER TABLE ONLY public.space_storage_usage
    ADD CONSTRAINT space_storage_usage_pkey PRIMARY KEY (space_id);

ALTER TABLE ONLY public.space_task_activity
    ADD CONSTRAINT space_task_activity_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_task_counters
    ADD CONSTRAINT space_task_counters_pkey PRIMARY KEY (space_id);

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_id_space_unique UNIQUE (id, space_id);

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_key_unique UNIQUE (space_id, task_key);

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_number_unique UNIQUE (space_id, task_number);

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_upload_reservations
    ADD CONSTRAINT space_upload_reservations_pkey PRIMARY KEY (upload_id);

ALTER TABLE ONLY public.space_workflow_action_journal
    ADD CONSTRAINT space_workflow_action_journal_pkey PRIMARY KEY (idempotency_key);

ALTER TABLE ONLY public.space_workflow_resource_leases
    ADD CONSTRAINT space_workflow_resource_leases_pkey PRIMARY KEY (resource_key);

ALTER TABLE ONLY public.space_workflow_versions
    ADD CONSTRAINT space_workflow_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.space_workflow_versions
    ADD CONSTRAINT space_workflow_versions_workflow_id_checksum_sha256_key UNIQUE (workflow_id, checksum_sha256);

ALTER TABLE ONLY public.space_workflow_versions
    ADD CONSTRAINT space_workflow_versions_workflow_id_version_key UNIQUE (workflow_id, version);

ALTER TABLE ONLY public.space_workflows
    ADD CONSTRAINT space_workflows_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.spaces
    ADD CONSTRAINT spaces_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.trusted_device_request_nonces
    ADD CONSTRAINT trusted_device_request_nonces_pkey PRIMARY KEY (device_id, nonce);

ALTER TABLE ONLY public.trusted_devices
    ADD CONSTRAINT trusted_devices_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.trusted_devices
    ADD CONSTRAINT trusted_devices_user_id_public_key_key UNIQUE (user_id, public_key);

ALTER TABLE ONLY public.user_app_activity
    ADD CONSTRAINT user_app_activity_pkey PRIMARY KEY (user_id, app_id);

ALTER TABLE ONLY public.user_global_home_activity
    ADD CONSTRAINT user_global_home_activity_pkey PRIMARY KEY (user_id, activity_date);

ALTER TABLE ONLY public.user_home_activity
    ADD CONSTRAINT user_home_activity_pkey PRIMARY KEY (user_id, space_id, activity_date);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_pkey PRIMARY KEY (id);

CREATE INDEX abuse_blocks_active_idx ON public.abuse_blocks USING btree (blocked_until);

CREATE INDEX account_deletion_provider_resources_due_idx ON public.account_deletion_provider_resources USING btree (request_id, available_at, kind, resource_id) WHERE (state = 'pending'::text);

CREATE UNIQUE INDEX account_deletion_requests_active_user_idx ON public.account_deletion_requests USING btree (user_id) WHERE (status = ANY (ARRAY['processing'::text, 'scheduled'::text]));

CREATE INDEX account_deletion_requests_due_idx ON public.account_deletion_requests USING btree (status, purge_after);

CREATE INDEX account_deletion_steps_pending_idx ON public.account_deletion_steps USING btree (available_at, request_id, step) WHERE (state = 'pending'::text);

CREATE INDEX account_deletion_steps_processing_idx ON public.account_deletion_steps USING btree (lease_expires_at) WHERE (state = 'processing'::text);

CREATE INDEX agent_conversation_events_conversation_idx ON public.misty_ask_conversation_events USING btree (conversation_id, id);

CREATE INDEX agent_conversations_retention_idx ON public.misty_ask_conversations USING btree (retention_expires_at);

CREATE INDEX agent_conversations_user_recent_idx ON public.misty_ask_conversations USING btree (user_id, updated_at DESC) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX agent_intervention_call ON public.ai_intervention_waits USING btree (run_id, call_id);

CREATE UNIQUE INDEX agent_intervention_pending ON public.ai_intervention_waits USING btree (run_id) WHERE (state = 'pending'::text);

CREATE INDEX agent_model_turn_invocation_idx ON public.agent_model_turn_claims USING btree (ai_invocation_id) WHERE (ai_invocation_id IS NOT NULL);

CREATE INDEX agent_model_turn_space_idx ON public.agent_model_turn_claims USING btree (space_run_id) WHERE (space_run_id IS NOT NULL);

CREATE INDEX agent_run_contexts_run_idx ON public.agent_run_contexts USING btree (run_id, kind) WHERE (state = 'attached'::text);

CREATE INDEX agent_run_jobs_active_agent_idx ON public.agent_run_jobs USING btree (agent_id, created_at) WHERE (state = ANY (ARRAY['leased'::text, 'dispatched'::text]));

CREATE INDEX agent_run_jobs_claim_idx ON public.agent_run_jobs USING btree (available_at, created_at) WHERE (state = ANY (ARRAY['queued'::text, 'leased'::text]));

CREATE INDEX agent_run_tool_approvals_owner_pending_idx ON public.agent_run_tool_approvals USING btree (owner_user_id, created_at) WHERE (state = 'pending'::text);

CREATE INDEX agent_runtime_deliveries_due ON public.agent_runtime_deliveries USING btree (available_at) WHERE (state = ANY (ARRAY['pending'::text, 'leased'::text]));

CREATE INDEX agent_sdk_bindings_ai_invocation_idx ON public.agent_sdk_capability_bindings USING btree (ai_invocation_id) WHERE (ai_invocation_id IS NOT NULL);

CREATE INDEX agent_sdk_bindings_space_run_idx ON public.agent_sdk_capability_bindings USING btree (space_run_id) WHERE (space_run_id IS NOT NULL);

CREATE INDEX agent_toolbox_action_journal_instance_idx ON public.agent_toolbox_action_journal USING btree (agent_instance_id, created_at DESC) WHERE (agent_instance_id IS NOT NULL);

CREATE INDEX agent_toolbox_action_journal_run_idx ON public.agent_toolbox_action_journal USING btree (run_id, created_at) WHERE (run_id IS NOT NULL);

CREATE INDEX agent_toolbox_action_journal_user_created_idx ON public.agent_toolbox_action_journal USING btree (user_id, created_at DESC);

CREATE INDEX ai_artifacts_user_created_idx ON public.ai_artifacts USING btree (user_id, created_at DESC);

CREATE INDEX ai_cleanup_jobs_due_idx ON public.ai_cleanup_jobs USING btree (available_at, created_at) WHERE (state = ANY (ARRAY['queued'::text, 'failed'::text]));

CREATE INDEX ai_conversation_attachments_conversation_idx ON public.ai_conversation_attachments USING btree (conversation_id, created_at) WHERE (lifecycle_state = 'ready'::text);

CREATE INDEX ai_conversation_attachments_expiry_idx ON public.ai_conversation_attachments USING btree (expires_at) WHERE ((expires_at IS NOT NULL) AND (lifecycle_state <> 'deleted'::text));

CREATE INDEX ai_intervention_waits_due ON public.ai_intervention_waits USING btree (expires_at) WHERE (state = 'pending'::text);

CREATE INDEX ai_invocation_contexts_user_active_idx ON public.ai_invocation_contexts USING btree (user_id, updated_at DESC) WHERE (state = 'attached'::text);

CREATE INDEX ai_invocation_device_waits_due ON public.ai_invocations USING btree (device_wait_expires_at) WHERE (state = 'awaiting_device'::text);

CREATE UNIQUE INDEX ai_invocation_events_receipt ON public.ai_invocation_events USING btree (invocation_id, receipt_key) WHERE (receipt_key IS NOT NULL);

CREATE INDEX ai_invocation_runtime_observation_due ON public.ai_invocations USING btree (runtime_observed_at, runtime_heartbeat_at) WHERE ((runtime_run_id <> ''::text) AND (state = ANY (ARRAY['running'::text, 'awaiting_approval'::text, 'awaiting_device'::text])));

CREATE INDEX ai_invocations_expiry_idx ON public.ai_invocations USING btree (expires_at) WHERE (expires_at IS NOT NULL);

CREATE UNIQUE INDEX ai_invocations_runtime_run_idx ON public.ai_invocations USING btree (runtime_run_id) WHERE (runtime_run_id <> ''::text);

CREATE INDEX ai_invocations_space_created_idx ON public.ai_invocations USING btree (space_id, created_at DESC) WHERE (space_id IS NOT NULL);

CREATE INDEX ai_invocations_user_updated_idx ON public.ai_invocations USING btree (user_id, updated_at DESC);

CREATE INDEX ai_recaps_due_idx ON public.ai_recaps USING btree (next_run_at) WHERE enabled;

CREATE INDEX ai_retrieval_chunks_embedding_idx ON public.ai_retrieval_chunks USING hnsw (embedding public.vector_cosine_ops) WHERE (embedding IS NOT NULL);

CREATE INDEX ai_retrieval_chunks_lexical_idx ON public.ai_retrieval_chunks USING gin (lexical);

CREATE INDEX ai_retrieval_documents_space_idx ON public.ai_retrieval_documents USING btree (space_id, source_kind, source_id) WHERE (lifecycle_state = 'active'::text);

CREATE INDEX ai_retrieval_embedding_due ON public.ai_retrieval_chunks USING btree (embedding_lease_until, updated_at) WHERE (embedding IS NULL);

CREATE INDEX billing_adapter_intents_expired ON public.billing_adapter_intents USING btree (expires_at) WHERE (state = ANY (ARRAY['pending'::text, 'abandoned'::text]));

CREATE INDEX billing_adapter_outbox_due ON public.billing_adapter_outbox USING btree (available_at, created_at) WHERE (delivered_at IS NULL);

CREATE INDEX billing_adapter_reservations_operation ON public.billing_adapter_reservations USING btree (account_id, ((admission ->> 'operation_id'::text)));

CREATE INDEX browser_sync_connection_expiry ON public.browser_sync_connections USING btree (expires_at);

CREATE INDEX browser_sync_ticket_expiry ON public.browser_sync_tickets USING btree (expires_at);

CREATE INDEX cloud_connections_connected_account_idx ON public.cloud_connections USING btree (connected_account_id) WHERE (revoked_at IS NULL);

CREATE INDEX cloud_connections_user_idx ON public.cloud_connections USING btree (user_id) WHERE (revoked_at IS NULL);

CREATE INDEX cloud_credential_handoffs_expiry_idx ON public.cloud_credential_handoffs USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX cloud_oauth_states_expiry_idx ON public.cloud_oauth_states USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX connected_account_oauth_states_expiry_idx ON public.connected_account_oauth_states USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX connected_accounts_owner_idx ON public.connected_accounts USING btree (user_id, provider, status, updated_at DESC);

CREATE INDEX connection_authorization_requests_expiry_idx ON public.connection_authorization_requests USING btree (expires_at);

CREATE INDEX connection_authorization_requests_owner_idx ON public.connection_authorization_requests USING btree (user_id, created_at DESC);

CREATE UNIQUE INDEX device_pairing_sessions_live_creator_idx ON public.device_pairing_sessions USING btree (creator_device_id) WHERE (state = ANY (ARRAY['pending'::text, 'redeemed'::text]));

CREATE INDEX device_pairing_sessions_manual_code_idx ON public.device_pairing_sessions USING btree (manual_code_hash) WHERE (state = 'pending'::text);

CREATE INDEX device_pairs_device_one_active_idx ON public.device_pairs USING btree (first_device_id) WHERE (state = 'active'::text);

CREATE INDEX device_pairs_device_two_active_idx ON public.device_pairs USING btree (second_device_id) WHERE (state = 'active'::text);

CREATE INDEX device_presence_owner_online_idx ON public.device_presence USING btree (owner_user_id, last_heartbeat_at DESC);

CREATE UNIQUE INDEX figma_comment_audit_idempotency_idx ON public.figma_comment_audit USING btree (binding_id, idempotency_key) WHERE (idempotency_key <> ''::text);

CREATE INDEX figma_comment_audit_space_idx ON public.figma_comment_audit USING btree (space_id, created_at DESC);

CREATE INDEX figma_content_records_query_idx ON public.figma_content_records USING btree (space_id, binding_id, record_type, occurred_at DESC, id) WHERE (deleted_at IS NULL);

CREATE INDEX figma_space_bindings_space_idx ON public.figma_space_bindings USING btree (space_id, status, display_name);

CREATE INDEX github_app_installations_space_idx ON public.github_app_installations USING btree (space_id, status, account_login);

CREATE INDEX github_app_setup_states_expiry_idx ON public.github_app_setup_states USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX github_code_workspaces_space_idx ON public.github_code_workspaces USING btree (space_id, status, full_name);

CREATE INDEX github_credential_handoffs_expiry_idx ON public.github_credential_handoffs USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX github_mutation_audit_space_idx ON public.github_mutation_audit USING btree (space_id, created_at DESC);

CREATE INDEX github_repository_records_query_idx ON public.github_repository_records USING btree (space_id, workspace_id, record_type, occurred_at DESC, id) WHERE (deleted_at IS NULL);

CREATE INDEX idx_auth_handoff_tokens_expires_at ON public.auth_handoff_tokens USING btree (expires_at);

CREATE INDEX idx_password_reset_tokens_expires_at ON public.password_reset_tokens USING btree (expires_at);

CREATE INDEX idx_sessions_expires_at ON public.sessions USING btree (expires_at);

CREATE INDEX idx_sessions_user_id ON public.sessions USING btree (user_id);

CREATE UNIQUE INDEX library_blobs_domain_digest_idx ON public.library_blobs USING btree (security_domain_id, sha256, byte_size) WHERE (lifecycle_state <> 'deleted'::text);

CREATE INDEX library_derivatives_file_idx ON public.library_derivatives USING btree (source_file_id, kind);

CREATE INDEX library_files_blob_idx ON public.library_files USING btree (blob_id);

CREATE INDEX library_files_capture_idx ON public.library_files USING btree (intrinsic_capture_at, id) WHERE (intrinsic_capture_at IS NOT NULL);

CREATE INDEX library_files_discovery_search_idx ON public.library_files USING gin (to_tsvector('simple'::regconfig, ((original_filename || ' '::text) || (intrinsic_metadata)::text)));

CREATE INDEX library_files_domain_idx ON public.library_files USING btree (security_domain_id, created_at DESC);

CREATE INDEX library_legal_holds_target_idx ON public.library_legal_holds USING btree (target_kind, target_id) WHERE active;

CREATE INDEX library_processing_jobs_claim_idx ON public.library_processing_jobs USING btree (state, available_at, priority DESC, created_at);

CREATE INDEX library_reauthentication_grants_lookup_idx ON public.library_reauthentication_grants USING btree (user_id, space_id, scope, expires_at DESC);

CREATE INDEX library_recovery_tombstones_due_idx ON public.library_recovery_tombstones USING btree (lifecycle_state, recover_until);

CREATE INDEX mail_action_audit_draft_outcome_idx ON public.mail_action_audit USING btree (connection_id, target_id, user_id) WHERE ((target_type = 'draft'::text) AND ((completed_at IS NULL) OR (error_code = 'mail_operation_incomplete'::text) OR ((action = 'draft_send'::text) AND success)));

CREATE INDEX mail_action_audit_owner_idx ON public.mail_action_audit USING btree (user_id, created_at DESC, id DESC);

CREATE INDEX mail_action_audit_unfinished_idx ON public.mail_action_audit USING btree (created_at, id) WHERE (completed_at IS NULL);

CREATE INDEX mcp_discovery_snapshots_connection_idx ON public.mcp_discovery_snapshots USING btree (connection_id, discovered_at DESC);

CREATE INDEX mcp_oauth_credentials_owner_idx ON public.mcp_oauth_credentials USING btree (owner_user_id, updated_at DESC);

CREATE INDEX mcp_oauth_states_expiry_idx ON public.mcp_oauth_states USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX mcp_remote_connections_owner_idx ON public.mcp_remote_connections USING btree (owner_user_id, status, updated_at DESC);

CREATE INDEX mcp_remote_tools_connection_idx ON public.mcp_remote_tools USING btree (connection_id, removed_at, schema_status, remote_name);

CREATE INDEX mcp_tool_execution_audit_agent_idx ON public.mcp_tool_execution_audit USING btree (agent_id, created_at DESC);

CREATE INDEX media_search_segments_device_asset_time_idx ON public.media_search_segments USING btree (user_id, device_id, asset_id, start_ms);

CREATE INDEX media_search_segments_device_idx ON public.media_search_segments USING btree (user_id, device_id);

CREATE INDEX media_search_segments_lexical_idx ON public.media_search_segments USING gin (search_tsv);

CREATE INDEX media_search_segments_semantic_idx ON public.media_search_segments USING hnsw (embedding public.vector_cosine_ops) WHERE (embedding IS NOT NULL);

CREATE INDEX misty_conversation_focus_lookup_idx ON public.misty_conversation_focus USING btree (user_id, conversation_id, space_id, updated_at DESC);

CREATE INDEX misty_conversation_pending_actions_lookup_idx ON public.misty_conversation_pending_actions USING btree (user_id, conversation_id, space_id, updated_at DESC);

CREATE INDEX misty_conversations_agent_idx ON public.misty_ask_conversations USING btree (user_id, agent_id, space_id, updated_at DESC);

CREATE INDEX misty_memories_active_space_idx ON public.misty_memories USING btree (user_id, space_id, updated_at DESC) WHERE ((forgotten_at IS NULL) AND (space_id IS NOT NULL));

CREATE INDEX misty_memories_active_user_idx ON public.misty_memories USING btree (user_id, updated_at DESC) WHERE (forgotten_at IS NULL);

CREATE INDEX misty_memories_agent_idx ON public.misty_memories USING btree (user_id, agent_id, space_id) WHERE (forgotten_at IS NULL);

CREATE INDEX native_calendar_work_due ON public.space_calendar_sources USING btree (native_sync_available_at, id) WHERE ((execution_owner = 'hono'::text) AND (disabled_at IS NULL) AND (status <> 'disabled'::text));

CREATE INDEX native_task_effects_due ON public.native_task_effects USING btree (available_at, id) WHERE (state = 'pending'::text);

CREATE INDEX object_deletion_jobs_creator_idx ON public.object_deletion_jobs USING btree (created_by_user_id) WHERE (created_by_user_id IS NOT NULL);

CREATE INDEX object_deletion_jobs_due_idx ON public.object_deletion_jobs USING btree (not_before, lease_expires_at);

CREATE INDEX password_recovery_jobs_email ON public.password_recovery_jobs USING btree (email, created_at DESC, id DESC);

CREATE INDEX password_recovery_jobs_expiry ON public.password_recovery_jobs USING btree (expires_at);

CREATE INDEX password_recovery_jobs_leases ON public.password_recovery_jobs USING btree (lease_expires_at) WHERE (state = 'processing'::text);

CREATE INDEX password_recovery_jobs_pending ON public.password_recovery_jobs USING btree (available_at, created_at, id) WHERE (state = 'pending'::text);

CREATE INDEX personal_agent_mcp_tools_agent_idx ON public.misty_ask_mcp_tools USING btree (agent_id, enabled, stable_name);

CREATE INDEX personal_agent_versions_checksum_idx ON public.misty_ask_identity_versions USING btree (agent_id, checksum_sha256);

CREATE UNIQUE INDEX personal_agents_one_managed_misty_per_owner_idx ON public.misty_ask_identities USING btree (owner_user_id) WHERE (system_managed AND (deleted_at IS NULL));

CREATE INDEX personal_agents_owner_recent_idx ON public.misty_ask_identities USING btree (owner_user_id, updated_at DESC) WHERE (deleted_at IS NULL);

CREATE INDEX provider_content_records_query_idx ON public.provider_content_records USING btree (space_id, provider, occurred_at DESC, id) WHERE (deleted_at IS NULL);

CREATE INDEX provider_oauth_states_expiry_idx ON public.provider_oauth_states USING btree (expires_at) WHERE (consumed_at IS NULL);

CREATE INDEX provider_shared_resources_space_idx ON public.provider_shared_resources USING btree (space_id, provider, status, display_name);

CREATE INDEX realtime_tickets_expiry_idx ON public.realtime_tickets USING btree (expires_at);

CREATE UNIQUE INDEX sdk_tool_approval_effect_idx ON public.agent_run_tool_approvals USING btree (invocation_id, tool_call_id) WHERE (invocation_id IS NOT NULL);

CREATE UNIQUE INDEX security_domains_personal_owner_idx ON public.security_domains USING btree (owner_user_id) WHERE (kind = 'personal'::text);

CREATE UNIQUE INDEX security_domains_space_idx ON public.security_domains USING btree (space_id) WHERE (kind = 'space'::text);

CREATE UNIQUE INDEX smart_library_active_client_root ON public.smart_library_folders USING btree (user_id, client_library_id) WHERE (deleted_at IS NULL);

CREATE INDEX smart_library_assets_active_index_claim_idx ON public.smart_library_assets USING btree (user_id, index_claimed_at) WHERE (index_status = 'processing'::text);

CREATE INDEX smart_library_assets_fingerprint ON public.smart_library_assets USING btree (folder_id, fingerprint);

CREATE INDEX smart_library_assets_reindex_idx ON public.smart_library_assets USING btree (user_id, folder_id, index_status, updated_at, asset_id);

CREATE INDEX smart_library_assets_search_tsv_idx ON public.smart_library_assets USING gin (search_tsv);

CREATE INDEX smart_library_assets_semantic_hnsw_idx ON public.smart_library_assets USING hnsw (semantic_embedding public.vector_cosine_ops) WITH (m='16', ef_construction='96') WHERE ((semantic_embedding IS NOT NULL) AND (status = 'analyzed'::text));

CREATE INDEX smart_library_assets_status ON public.smart_library_assets USING btree (folder_id, status);

CREATE INDEX smart_library_reindex_jobs_user_status ON public.smart_library_reindex_jobs USING btree (user_id, status, created_at DESC);

CREATE INDEX social_bindings_connection_idx ON public.social_bindings USING btree (connection_id, status);

CREATE INDEX social_bindings_resource_idx ON public.social_bindings USING btree (provider, external_resource_id) WHERE (disabled_at IS NULL);

CREATE INDEX social_outbound_commands_ready_idx ON public.social_outbound_commands USING btree (available_at, created_at) WHERE (state = 'queued'::text);

CREATE INDEX social_scheduled_messages_due_idx ON public.social_scheduled_messages USING btree (scheduled_at) WHERE (status = 'scheduled'::text);

CREATE UNIQUE INDEX social_send_authorities_active_idx ON public.social_send_authorities USING btree (user_id, connection_id, COALESCE(binding_id, ''::text)) WHERE (revoked_at IS NULL);

CREATE UNIQUE INDEX space_album_folders_name_idx ON public.space_album_folders USING btree (space_id, COALESCE(parent_folder_id, ''::text), lower(name));

CREATE INDEX space_album_folders_order_idx ON public.space_album_folders USING btree (space_id, parent_folder_id, "position", id);

CREATE INDEX space_albums_folder_order_idx ON public.space_albums USING btree (space_id, folder_id, "position", id);

CREATE INDEX space_calendar_events_range_idx ON public.space_calendar_events USING btree (space_id, starts_at, ends_at) WHERE (removed_at IS NULL);

CREATE INDEX space_calendar_sources_health_idx ON public.space_calendar_sources USING btree (status, watch_expires_at, last_reconciled_at);

CREATE INDEX space_conversation_members_agent_idx ON public.space_conversation_members USING btree (agent_id, conversation_id) WHERE (actor_kind = 'agent'::text);

CREATE UNIQUE INDEX space_conversation_members_agent_unique ON public.space_conversation_members USING btree (conversation_id, agent_id) WHERE (actor_kind = 'agent'::text);

CREATE UNIQUE INDEX space_conversation_members_person_unique ON public.space_conversation_members USING btree (conversation_id, user_id) WHERE (actor_kind = 'person'::text);

CREATE INDEX space_conversation_members_user_idx ON public.space_conversation_members USING btree (user_id, conversation_id);

CREATE INDEX space_conversation_reads_user_idx ON public.space_conversation_reads USING btree (user_id, conversation_id);

CREATE UNIQUE INDEX space_conversations_direct_unique ON public.space_conversations USING btree (space_id, direct_user_id, direct_agent_id) WHERE ((kind = 'direct'::text) AND (direct_agent_id IS NOT NULL));

CREATE UNIQUE INDEX space_conversations_discord_resource_idx ON public.space_conversations USING btree (space_id, external_resource_id) WHERE ((origin = 'discord'::text) AND (external_resource_id <> ''::text));

CREATE UNIQUE INDEX space_conversations_instagram_resource_idx ON public.space_conversations USING btree (space_id, external_resource_id) WHERE ((origin = 'instagram'::text) AND (external_resource_id <> ''::text));

CREATE UNIQUE INDEX space_conversations_slack_resource_idx ON public.space_conversations USING btree (space_id, external_resource_id) WHERE ((origin = 'slack'::text) AND (external_resource_id <> ''::text));

CREATE INDEX space_conversations_space_idx ON public.space_conversations USING btree (space_id, updated_at DESC);

CREATE INDEX space_discord_links_channel_idx ON public.space_discord_links USING btree (guild_id, channel_id) WHERE (disabled_at IS NULL);

CREATE INDEX space_discord_links_space_idx ON public.space_discord_links USING btree (space_id) WHERE (disabled_at IS NULL);

CREATE INDEX space_drawing_assets_cleanup_idx ON public.space_drawing_assets USING btree (lifecycle_state, deleted_at) WHERE (lifecycle_state <> 'ready'::text);

CREATE INDEX space_drawing_assets_drawing_idx ON public.space_drawing_assets USING btree (drawing_id, lifecycle_state);

CREATE INDEX space_drawing_control_outbox_pending_idx ON public.space_drawing_control_outbox USING btree (next_attempt_at) WHERE (delivered_at IS NULL);

CREATE INDEX space_drawings_creator_idx ON public.space_drawings USING btree (creator_user_id, lifecycle_state);

CREATE INDEX space_drawings_space_recent_idx ON public.space_drawings USING btree (space_id, lifecycle_state, updated_at DESC);

CREATE INDEX space_events_replay_idx ON public.space_events USING btree (id, created_at);

CREATE INDEX space_events_space_idx ON public.space_events USING btree (space_id, id);

CREATE INDEX space_inbox_user_idx ON public.space_inbox_items USING btree (user_id, kind, id DESC);

CREATE INDEX space_integrations_space_idx ON public.space_integrations USING btree (space_id, provider);

CREATE INDEX space_invitation_delivery_jobs_due_idx ON public.space_invitation_delivery_jobs USING btree (available_at, invite_id) WHERE (state = 'pending'::text);

CREATE INDEX space_invitation_delivery_jobs_lease_idx ON public.space_invitation_delivery_jobs USING btree (lease_expires_at) WHERE (state = 'processing'::text);

CREATE UNIQUE INDEX space_invitations_active_email_idx ON public.space_invitations USING btree (space_id, lower(invited_email)) WHERE ((revoked_at IS NULL) AND (consumed_at IS NULL));

CREATE INDEX space_invitations_active_user_idx ON public.space_invitations USING btree (invited_user_id, expires_at) WHERE ((revoked_at IS NULL) AND (consumed_at IS NULL));

CREATE UNIQUE INDEX space_invitations_token_hash_idx ON public.space_invitations USING btree (token_hash);

CREATE INDEX space_invitations_user_idx ON public.space_invitations USING btree (invited_user_id, expires_at);

CREATE INDEX space_library_asset_stack_members_item_idx ON public.space_library_asset_stack_members USING btree (space_library_item_id);

CREATE INDEX space_library_asset_stacks_space_idx ON public.space_library_asset_stacks USING btree (space_id, kind, created_at DESC) WHERE (lifecycle_state = 'ready'::text);

CREATE INDEX space_library_audit_space_idx ON public.space_library_audit_events USING btree (space_id, created_at DESC);

CREATE INDEX space_library_grants_destination_idx ON public.space_library_grants USING btree (destination_space_id, state, created_at DESC);

CREATE INDEX space_library_imports_destination_idx ON public.space_library_imports USING btree (destination_space_id, created_at DESC);

CREATE INDEX space_library_item_views_recent_idx ON public.space_library_item_views USING btree (space_id, user_id, last_viewed_at DESC);

CREATE INDEX space_library_items_discovery_search_idx ON public.space_library_items USING gin (to_tsvector('simple'::regconfig, ((((display_name || ' '::text) || caption) || ' '::text) || (tags)::text)));

CREATE INDEX space_library_items_favorite_added_idx ON public.space_library_items USING btree (space_id, added_at DESC, id DESC) WHERE ((lifecycle_state = 'ready'::text) AND favorite);

CREATE INDEX space_library_items_file_idx ON public.space_library_items USING btree (file_id);

CREATE INDEX space_library_items_hidden_added_idx ON public.space_library_items USING btree (space_id, added_at DESC, id DESC) WHERE ((lifecycle_state = 'ready'::text) AND hidden);

CREATE INDEX space_library_items_space_idx ON public.space_library_items USING btree (space_id, lifecycle_state, added_at DESC, id);

CREATE INDEX space_library_items_visible_added_idx ON public.space_library_items USING btree (space_id, hidden, added_at DESC, id DESC) WHERE (lifecycle_state = 'ready'::text);

CREATE INDEX space_library_search_documents_lexical_idx ON public.space_library_search_documents USING gin (search_tsv);

CREATE INDEX space_library_search_documents_semantic_idx ON public.space_library_search_documents USING hnsw (embedding public.vector_cosine_ops) WHERE ((embedding IS NOT NULL) AND (state = 'ready'::text));

CREATE INDEX space_library_uploads_conversation_idx ON public.space_library_uploads USING btree (conversation_id) WHERE (conversation_id IS NOT NULL);

CREATE INDEX space_library_uploads_drawing_idx ON public.space_library_uploads USING btree (drawing_id) WHERE (drawing_id IS NOT NULL);

CREATE INDEX space_library_uploads_expiry_idx ON public.space_library_uploads USING btree (state, expires_at);

CREATE INDEX space_library_uploads_note_idx ON public.space_library_uploads USING btree (note_id) WHERE (note_id IS NOT NULL);

CREATE INDEX space_library_uploads_user_idx ON public.space_library_uploads USING btree (space_id, user_id, created_at DESC);

CREATE UNIQUE INDEX space_members_one_owner_idx ON public.space_members USING btree (space_id) WHERE (role = 'owner'::text);

CREATE INDEX space_members_user_idx ON public.space_members USING btree (user_id);

CREATE INDEX space_message_attachments_message_idx ON public.space_message_attachments USING btree (message_id);

CREATE INDEX space_message_attachments_space_idx ON public.space_message_attachments USING btree (space_id, created_at DESC);

CREATE INDEX space_message_library_references_space_idx ON public.space_message_library_references USING btree (space_id, message_id);

CREATE INDEX space_message_reactions_message_idx ON public.space_message_reactions USING btree (message_id, created_at);

CREATE INDEX space_message_reactions_space_idx ON public.space_message_reactions USING btree (space_id, created_at DESC);

CREATE INDEX space_messages_expiry_idx ON public.space_messages USING btree (expires_at);

CREATE INDEX space_messages_history_idx ON public.space_messages USING btree (space_id, conversation_id, seq DESC);

CREATE INDEX space_messages_reply_idx ON public.space_messages USING btree (reply_to_message_id);

CREATE UNIQUE INDEX space_messages_slack_external_idx ON public.space_messages USING btree (space_id, ((origin ->> 'external_id'::text))) WHERE (((origin ->> 'system'::text) = 'slack'::text) AND ((origin ->> 'external_id'::text) <> ''::text));

CREATE UNIQUE INDEX space_messages_social_external_idx ON public.space_messages USING btree (space_id, social_provider, social_external_id) WHERE ((social_provider IS NOT NULL) AND (social_external_id <> ''::text));

CREATE INDEX space_native_calendar_events_range_idx ON public.space_native_calendar_events USING btree (space_id, starts_at, ends_at) WHERE (archived_at IS NULL);

CREATE INDEX space_nodes_parent_idx ON public.space_nodes USING btree (space_id, parent_id, display_name);

CREATE INDEX space_note_assets_cleanup_idx ON public.space_note_assets USING btree (lifecycle_state, deleted_at) WHERE (lifecycle_state <> 'ready'::text);

CREATE INDEX space_note_assets_note_idx ON public.space_note_assets USING btree (note_id, lifecycle_state);

CREATE INDEX space_note_control_outbox_pending_idx ON public.space_note_control_outbox USING btree (next_attempt_at) WHERE (delivered_at IS NULL);

CREATE INDEX space_note_links_target_idx ON public.space_note_links USING btree (target_note_id, created_at DESC);

CREATE INDEX space_note_permissions_user_idx ON public.space_note_permissions USING btree (user_id);

CREATE INDEX space_notes_creator_idx ON public.space_notes USING btree (creator_user_id, lifecycle_state);

CREATE INDEX space_notes_purge_idx ON public.space_notes USING btree (purge_after) WHERE (purge_after IS NOT NULL);

CREATE INDEX space_notes_search_idx ON public.space_notes USING gin ((((setweight(to_tsvector('simple'::regconfig, COALESCE(title_projection, ''::text)), 'A'::"char") || setweight(to_tsvector('simple'::regconfig, COALESCE((shared_tags)::text, ''::text)), 'B'::"char")) || setweight(to_tsvector('simple'::regconfig, COALESCE(plain_text_projection, ''::text)), 'C'::"char"))));

CREATE INDEX space_notes_space_recent_idx ON public.space_notes USING btree (space_id, lifecycle_state, updated_at DESC);

CREATE UNIQUE INDEX space_person_observations_identity_idx ON public.space_person_observations USING btree (person_id, space_library_item_id, derivative_id) NULLS NOT DISTINCT;

CREATE INDEX space_person_observations_item_idx ON public.space_person_observations USING btree (space_library_item_id);

CREATE INDEX space_pinned_collections_order_idx ON public.space_pinned_collections USING btree (space_id, "position");

CREATE INDEX space_provider_credentials_owner_idx ON public.space_provider_credentials USING btree (user_id, space_id, provider);

CREATE INDEX space_rendition_reservations_usage_idx ON public.space_rendition_reservations USING btree (space_id, state, expires_at);

CREATE INDEX space_resolve_tickets_expiry_idx ON public.space_resolve_tickets USING btree (expires_at);

CREATE UNIQUE INDEX space_roadmap_edges_endpoint_unique_idx ON public.space_roadmap_edges USING btree (roadmap_id, source_kind, source_id, target_kind, target_id, edge_type);

CREATE INDEX space_roadmap_edges_graph_idx ON public.space_roadmap_edges USING btree (roadmap_id, source_goal_id, target_goal_id);

CREATE INDEX space_roadmap_goal_tasks_task_idx ON public.space_roadmap_goal_tasks USING btree (task_id, goal_id);

CREATE INDEX space_roadmap_goals_order_idx ON public.space_roadmap_goals USING btree (milestone_id, rank, id) WHERE (archived_at IS NULL);

CREATE INDEX space_roadmap_goals_target_idx ON public.space_roadmap_goals USING btree (space_id, target_date, id) WHERE ((archived_at IS NULL) AND (target_date IS NOT NULL));

CREATE INDEX space_roadmap_milestones_order_idx ON public.space_roadmap_milestones USING btree (roadmap_id, rank, id) WHERE (archived_at IS NULL);

CREATE INDEX space_roadmap_node_definitions_space_idx ON public.space_roadmap_node_definitions USING btree (space_id, name, id) WHERE (archived_at IS NULL);

CREATE INDEX space_roadmap_nodes_graph_idx ON public.space_roadmap_nodes USING btree (roadmap_id, milestone_id, id) WHERE (archived_at IS NULL);

CREATE INDEX space_roadmap_nodes_target_idx ON public.space_roadmap_nodes USING btree (space_id, target_date, id) WHERE ((archived_at IS NULL) AND (target_date IS NOT NULL));

CREATE INDEX space_roadmaps_space_idx ON public.space_roadmaps USING btree (space_id, updated_at DESC, id) WHERE (archived_at IS NULL);

CREATE UNIQUE INDEX space_roles_everyone_idx ON public.space_roles USING btree (space_id) WHERE is_everyone;

CREATE INDEX space_run_actions_run_idx ON public.space_run_actions USING btree (run_id, created_at);

CREATE INDEX space_run_approvals_run_idx ON public.space_run_approvals USING btree (run_id, created_at);

CREATE INDEX space_runs_agent_idx ON public.space_runs USING btree (agent_id, created_at DESC);

CREATE UNIQUE INDEX space_runs_agent_source_message_once ON public.space_runs USING btree (source_message_id, agent_id) WHERE ((source_message_id IS NOT NULL) AND (trigger_kind = 'direct_instruction'::text));

CREATE UNIQUE INDEX space_runs_ai_handoff_idempotency_idx ON public.space_runs USING btree (requesting_member_id, ((input ->> 'ai_idempotency_key'::text))) WHERE ((source_type = 'agent_console'::text) AND (input ? 'ai_idempotency_key'::text));

CREATE UNIQUE INDEX space_runs_ai_idempotency_key_idx ON public.space_runs USING btree (owner_user_id, ((input ->> 'ai_idempotency_key'::text))) WHERE ((trigger_kind = 'direct_instruction'::text) AND (COALESCE((input ->> 'ai_idempotency_key'::text), ''::text) <> ''::text));

CREATE INDEX space_runs_personal_agent_runtime_idx ON public.space_runs USING btree (agent_id, created_at DESC) WHERE (trigger_kind = 'task_assignment'::text);

CREATE INDEX space_runs_requester_idx ON public.space_runs USING btree (requesting_member_id, created_at DESC);

CREATE UNIQUE INDEX space_runs_runtime_run_id_unique ON public.space_runs USING btree (runtime_run_id) WHERE (runtime_run_id <> ''::text);

CREATE INDEX space_runs_scope_idx ON public.space_runs USING btree (space_id, conversation_scope_kind, scope_conversation_id, created_at DESC);

CREATE INDEX space_runs_space_idx ON public.space_runs USING btree (space_id, created_at DESC);

CREATE UNIQUE INDEX space_runs_task_assignment_once_idx ON public.space_runs USING btree (source_task_id, agent_id, ((action_envelope ->> 'assignment_task_version'::text))) WHERE ((trigger_kind = 'task_assignment'::text) AND (source_task_id IS NOT NULL));

CREATE INDEX space_slack_links_channel_idx ON public.space_slack_links USING btree (team_id, channel_id) WHERE (disabled_at IS NULL);

CREATE INDEX space_slack_links_space_idx ON public.space_slack_links USING btree (space_id) WHERE (disabled_at IS NULL);

CREATE INDEX space_storage_contributions_usage_idx ON public.space_storage_contributions USING btree (space_id, user_id, state);

CREATE INDEX space_task_activity_task_idx ON public.space_task_activity USING btree (task_id, created_at, id);

CREATE INDEX space_tasks_agent_assignee_idx ON public.space_tasks USING btree (space_id, assignee_agent_id, archived_at, due_at) WHERE (assignee_agent_id IS NOT NULL);

CREATE INDEX space_tasks_assignee_idx ON public.space_tasks USING btree (space_id, assignee_user_id, archived_at, due_at);

CREATE INDEX space_tasks_board_idx ON public.space_tasks USING btree (space_id, status, rank, id) WHERE (archived_at IS NULL);

CREATE UNIQUE INDEX space_tasks_google_event_idx ON public.space_tasks USING btree (((calendar ->> 'source_id'::text)), ((calendar ->> 'google_event_id'::text))) WHERE (((calendar ->> 'google_event_id'::text) IS NOT NULL) AND (archived_at IS NULL));

CREATE INDEX space_tasks_list_idx ON public.space_tasks USING btree (space_id, archived_at, status, due_at, id);

CREATE INDEX space_tasks_priority_idx ON public.space_tasks USING btree (space_id, priority, status, rank) WHERE (archived_at IS NULL);

CREATE INDEX space_upload_reservations_usage_idx ON public.space_upload_reservations USING btree (space_id, user_id, state);

CREATE INDEX space_workflow_versions_space_idx ON public.space_workflow_versions USING btree (space_id, created_at DESC);

CREATE UNIQUE INDEX space_workflows_identifier_idx ON public.space_workflows USING btree (space_id, stable_identifier);

CREATE INDEX space_workflows_space_idx ON public.space_workflows USING btree (space_id, updated_at DESC);

CREATE INDEX spaces_lifecycle_owner_idx ON public.spaces USING btree (owner_user_id, lifecycle_state, created_at);

CREATE UNIQUE INDEX spaces_one_default_per_owner_idx ON public.spaces USING btree (owner_user_id) WHERE (is_default AND (lifecycle_state = 'active'::text));

CREATE INDEX spaces_owner_idx ON public.spaces USING btree (owner_user_id, created_at);

CREATE INDEX spaces_security_domain_idx ON public.spaces USING btree (security_domain_id);

CREATE INDEX trusted_device_request_nonces_expiry_idx ON public.trusted_device_request_nonces USING btree (expires_at);

CREATE INDEX trusted_devices_user_active_idx ON public.trusted_devices USING btree (user_id, last_seen_at DESC) WHERE (revoked_at IS NULL);

CREATE UNIQUE INDEX trusted_devices_user_p2p_endpoint_idx ON public.trusted_devices USING btree (user_id, p2p_endpoint_id) WHERE (p2p_endpoint_id IS NOT NULL);

CREATE INDEX user_app_activity_recent_idx ON public.user_app_activity USING btree (user_id, last_opened_at DESC);

CREATE INDEX user_home_activity_recent_idx ON public.user_home_activity USING btree (user_id, space_id, activity_date DESC);

CREATE UNIQUE INDEX users_avatar_object_key_idx ON public.users USING btree (avatar_object_key) WHERE (avatar_object_key IS NOT NULL);

CREATE UNIQUE INDEX users_email_normalized_unique_idx ON public.users USING btree (lower(email));

CREATE UNIQUE INDEX users_username_unique_idx ON public.users USING btree (lower(username));

CREATE INDEX workflow_device_execution_deadline_idx ON public.workflow_device_node_jobs USING btree (deadline_at) WHERE (state = ANY (ARRAY['queued'::text, 'leased'::text, 'executing'::text]));

CREATE INDEX workflow_device_node_jobs_claim_idx ON public.workflow_device_node_jobs USING btree (user_id, state, created_at);

CREATE UNIQUE INDEX workflow_device_node_jobs_invocation_attempt_idx ON public.workflow_device_node_jobs USING btree (invocation_id, node_id, attempt) WHERE (invocation_id IS NOT NULL);

CREATE UNIQUE INDEX workflow_device_node_jobs_run_attempt_idx ON public.workflow_device_node_jobs USING btree (run_id, node_id, attempt) WHERE (run_id IS NOT NULL);

CREATE TRIGGER ai_attachment_removed_objects AFTER DELETE ON public.ai_conversation_attachments FOR EACH ROW EXECUTE FUNCTION public.queue_removed_ai_attachment_objects();

CREATE TRIGGER ai_attachment_replaced_objects AFTER UPDATE OF object_key, model_object_key, lifecycle_state ON public.ai_conversation_attachments FOR EACH ROW EXECUTE FUNCTION public.queue_removed_ai_attachment_objects();

CREATE TRIGGER ai_index_provider_content AFTER INSERT OR DELETE OR UPDATE ON public.provider_content_records FOR EACH ROW EXECUTE FUNCTION public.misty_ai_index_provider_record();

CREATE TRIGGER ai_index_space_native_calendar AFTER INSERT OR DELETE OR UPDATE ON public.space_native_calendar_events FOR EACH ROW EXECUTE FUNCTION public.misty_ai_index_native_calendar();

CREATE TRIGGER ai_index_space_notes AFTER INSERT OR DELETE OR UPDATE ON public.space_notes FOR EACH ROW EXECUTE FUNCTION public.misty_ai_index_note();

CREATE TRIGGER ai_index_space_provider_calendar AFTER INSERT OR DELETE OR UPDATE ON public.space_calendar_events FOR EACH ROW EXECUTE FUNCTION public.misty_ai_index_provider_calendar();

CREATE TRIGGER ai_index_space_roadmaps AFTER INSERT OR DELETE OR UPDATE ON public.space_roadmaps FOR EACH ROW EXECUTE FUNCTION public.misty_ai_index_roadmap();

CREATE TRIGGER ai_index_space_tasks AFTER INSERT OR DELETE OR UPDATE ON public.space_tasks FOR EACH ROW EXECUTE FUNCTION public.misty_ai_index_task();

CREATE TRIGGER cancel_run_device_work AFTER UPDATE OF state ON public.ai_invocations FOR EACH ROW EXECUTE FUNCTION public.misty_cancel_run_device_work();

CREATE TRIGGER cancel_run_device_work AFTER UPDATE OF state ON public.space_runs FOR EACH ROW EXECUTE FUNCTION public.misty_cancel_run_device_work();

CREATE TRIGGER misty_agent_changes AFTER INSERT OR DELETE OR UPDATE ON public.misty_ask_identities FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('agents', 'owner_user_id');

CREATE TRIGGER misty_approval_changes AFTER INSERT OR DELETE OR UPDATE ON public.agent_run_tool_approvals FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('approvals', 'owner_user_id');

CREATE TRIGGER misty_intervention_changes AFTER INSERT OR DELETE OR UPDATE ON public.ai_intervention_waits FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('interventions', 'user_id');

CREATE TRIGGER misty_invocation_changes AFTER INSERT OR DELETE OR UPDATE OF state ON public.ai_invocations FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('invocations', 'user_id');

CREATE TRIGGER misty_job_changes AFTER INSERT OR DELETE OR UPDATE OF state ON public.workflow_device_node_jobs FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('jobs', 'user_id');

CREATE TRIGGER misty_run_approval_changes AFTER INSERT OR DELETE OR UPDATE ON public.space_run_approvals FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('approvals', 'requested_from_user_id');

CREATE TRIGGER misty_run_changes AFTER INSERT OR DELETE OR UPDATE OF state, progress ON public.space_runs FOR EACH ROW EXECUTE FUNCTION public.misty_notify_account_change('runs', 'requesting_member_id');

CREATE TRIGGER pause_execution_clock BEFORE UPDATE OF state ON public.ai_invocations FOR EACH ROW EXECUTE FUNCTION public.misty_pause_agent_execution_clock();

CREATE TRIGGER pause_execution_clock BEFORE UPDATE OF state ON public.space_runs FOR EACH ROW EXECUTE FUNCTION public.misty_pause_agent_execution_clock();

CREATE TRIGGER preserve_agent_runtime_pin BEFORE UPDATE ON public.ai_invocations FOR EACH ROW EXECUTE FUNCTION public.preserve_agent_runtime_pin();

CREATE TRIGGER preserve_agent_runtime_pin BEFORE UPDATE ON public.space_runs FOR EACH ROW EXECUTE FUNCTION public.preserve_agent_runtime_pin();

CREATE TRIGGER space_note_permissions_guard BEFORE INSERT OR UPDATE ON public.space_note_permissions FOR EACH ROW EXECUTE FUNCTION public.misty_note_permission_guard();

CREATE TRIGGER space_runs_default_owner BEFORE INSERT ON public.space_runs FOR EACH ROW EXECUTE FUNCTION public.misty_default_agent_run_owner();

CREATE TRIGGER space_tasks_clear_roadmap_manual_completion AFTER UPDATE OF status, archived_at ON public.space_tasks FOR EACH ROW EXECUTE FUNCTION public.clear_roadmap_manual_completion_for_reopened_task();

CREATE TRIGGER space_usage_owner_pool_sync AFTER INSERT OR DELETE OR UPDATE ON public.space_storage_usage FOR EACH ROW EXECUTE FUNCTION public.sync_owner_storage_from_space_usage();

CREATE TRIGGER spaces_owner_pool_sync AFTER UPDATE OF owner_user_id, lifecycle_state ON public.spaces FOR EACH ROW EXECUTE FUNCTION public.sync_owner_storage_after_transfer();

CREATE TRIGGER spaces_protect_default_delete BEFORE DELETE ON public.spaces FOR EACH ROW EXECUTE FUNCTION public.misty_protect_default_space();

CREATE TRIGGER spaces_protect_default_update BEFORE UPDATE OF is_default, owner_user_id, lifecycle_state ON public.spaces FOR EACH ROW EXECUTE FUNCTION public.misty_protect_default_space();

CREATE TRIGGER user_avatar_deleted AFTER DELETE ON public.users FOR EACH ROW EXECUTE FUNCTION public.queue_replaced_user_avatar();

CREATE TRIGGER user_avatar_replaced AFTER UPDATE OF avatar_object_key, avatar_version ON public.users FOR EACH ROW EXECUTE FUNCTION public.queue_replaced_user_avatar();

ALTER TABLE ONLY public.account_deletion_provider_resources
    ADD CONSTRAINT account_deletion_provider_resources_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.account_deletion_requests(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.account_deletion_requests
    ADD CONSTRAINT account_deletion_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.account_deletion_steps
    ADD CONSTRAINT account_deletion_steps_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.account_deletion_requests(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_conversation_events
    ADD CONSTRAINT agent_conversation_events_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.misty_ask_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_conversation_events
    ADD CONSTRAINT agent_conversation_events_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_conversations
    ADD CONSTRAINT agent_conversations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_model_turn_claims
    ADD CONSTRAINT agent_model_turn_claims_ai_invocation_id_fkey FOREIGN KEY (ai_invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_model_turn_claims
    ADD CONSTRAINT agent_model_turn_claims_space_run_id_fkey FOREIGN KEY (space_run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_model_turn_claims
    ADD CONSTRAINT agent_model_turn_claims_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_contexts
    ADD CONSTRAINT agent_run_contexts_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.agent_run_contexts
    ADD CONSTRAINT agent_run_contexts_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_contexts
    ADD CONSTRAINT agent_run_contexts_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_contexts
    ADD CONSTRAINT agent_run_contexts_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_jobs
    ADD CONSTRAINT agent_run_jobs_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.agent_run_tool_approvals
    ADD CONSTRAINT agent_run_tool_approvals_decided_by_user_id_fkey FOREIGN KEY (decided_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.agent_run_tool_approvals
    ADD CONSTRAINT agent_run_tool_approvals_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_tool_approvals
    ADD CONSTRAINT agent_run_tool_approvals_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_tool_approvals
    ADD CONSTRAINT agent_run_tool_approvals_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_runtime_deliveries
    ADD CONSTRAINT agent_runtime_deliveries_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_sdk_capability_bindings
    ADD CONSTRAINT agent_sdk_capability_bindings_ai_invocation_id_fkey FOREIGN KEY (ai_invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_sdk_capability_bindings
    ADD CONSTRAINT agent_sdk_capability_bindings_space_run_id_fkey FOREIGN KEY (space_run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_sdk_capability_bindings
    ADD CONSTRAINT agent_sdk_capability_bindings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_sdk_capability_bindings
    ADD CONSTRAINT agent_sdk_capability_bindings_user_id_provider_id_provider_fkey FOREIGN KEY (user_id, provider_id, provider_version) REFERENCES public.sdk_provider_versions(user_id, provider_id, version);

ALTER TABLE ONLY public.agent_sdk_capability_bindings
    ADD CONSTRAINT agent_sdk_capability_bindings_user_id_target_id_target_rev_fkey FOREIGN KEY (user_id, target_id, target_revision) REFERENCES public.sdk_target_versions(user_id, id, revision);

ALTER TABLE ONLY public.agent_toolbox_action_journal
    ADD CONSTRAINT agent_toolbox_action_journal_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_artifacts
    ADD CONSTRAINT ai_artifacts_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_artifacts
    ADD CONSTRAINT ai_artifacts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_cleanup_jobs
    ADD CONSTRAINT ai_cleanup_jobs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_conversation_attachments
    ADD CONSTRAINT ai_conversation_attachments_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.misty_ask_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_conversation_attachments
    ADD CONSTRAINT ai_conversation_attachments_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.ai_conversation_attachments
    ADD CONSTRAINT ai_conversation_attachments_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_feature_flags
    ADD CONSTRAINT ai_feature_flags_updated_by_user_id_fkey FOREIGN KEY (updated_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.ai_feedback
    ADD CONSTRAINT ai_feedback_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_feedback
    ADD CONSTRAINT ai_feedback_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_intervention_waits
    ADD CONSTRAINT ai_intervention_waits_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_intervention_waits
    ADD CONSTRAINT ai_intervention_waits_space_run_id_fkey FOREIGN KEY (space_run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_intervention_waits
    ADD CONSTRAINT ai_intervention_waits_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_invocation_contexts
    ADD CONSTRAINT ai_invocation_contexts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_invocation_events
    ADD CONSTRAINT ai_invocation_events_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_invocations
    ADD CONSTRAINT ai_invocations_agent_run_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.space_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.ai_invocations
    ADD CONSTRAINT ai_invocations_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.ai_invocations
    ADD CONSTRAINT ai_invocations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_recaps
    ADD CONSTRAINT ai_recaps_last_invocation_id_fkey FOREIGN KEY (last_invocation_id) REFERENCES public.ai_invocations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.ai_recaps
    ADD CONSTRAINT ai_recaps_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_retrieval_chunks
    ADD CONSTRAINT ai_retrieval_chunks_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.ai_retrieval_documents(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_retrieval_documents
    ADD CONSTRAINT ai_retrieval_documents_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_retrieval_documents
    ADD CONSTRAINT ai_retrieval_documents_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_retrieval_documents
    ADD CONSTRAINT ai_retrieval_documents_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_runtime_callback_receipts
    ADD CONSTRAINT ai_runtime_callback_receipts_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_surface_preferences
    ADD CONSTRAINT ai_surface_preferences_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ai_user_settings
    ADD CONSTRAINT ai_user_settings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.auth_handoff_tokens
    ADD CONSTRAINT auth_handoff_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_checkpoints
    ADD CONSTRAINT browser_sync_checkpoints_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_connections
    ADD CONSTRAINT browser_sync_connections_workspace_id_device_id_fkey FOREIGN KEY (workspace_id, device_id) REFERENCES public.browser_sync_devices(workspace_id, device_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_control_requests
    ADD CONSTRAINT browser_sync_control_requests_workspace_id_device_id_fkey FOREIGN KEY (workspace_id, device_id) REFERENCES public.browser_sync_devices(workspace_id, device_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_devices
    ADD CONSTRAINT browser_sync_devices_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_events
    ADD CONSTRAINT browser_sync_events_workspace_id_device_id_fkey FOREIGN KEY (workspace_id, device_id) REFERENCES public.browser_sync_devices(workspace_id, device_id);

ALTER TABLE ONLY public.browser_sync_events
    ADD CONSTRAINT browser_sync_events_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_receipts
    ADD CONSTRAINT browser_sync_receipts_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.browser_sync_workspaces(workspace_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_tickets
    ADD CONSTRAINT browser_sync_tickets_workspace_id_device_id_fkey FOREIGN KEY (workspace_id, device_id) REFERENCES public.browser_sync_devices(workspace_id, device_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.browser_sync_workspaces
    ADD CONSTRAINT browser_sync_workspaces_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.cloud_connections
    ADD CONSTRAINT cloud_connections_connected_account_id_fkey FOREIGN KEY (connected_account_id) REFERENCES public.connected_accounts(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.cloud_connections
    ADD CONSTRAINT cloud_connections_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.cloud_credential_handoffs
    ADD CONSTRAINT cloud_credential_handoffs_cloud_connection_id_fkey FOREIGN KEY (cloud_connection_id) REFERENCES public.cloud_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.cloud_credential_handoffs
    ADD CONSTRAINT cloud_credential_handoffs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.cloud_oauth_states
    ADD CONSTRAINT cloud_oauth_states_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.connected_account_oauth_states
    ADD CONSTRAINT connected_account_oauth_states_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.connected_accounts
    ADD CONSTRAINT connected_accounts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.connection_authorization_requests
    ADD CONSTRAINT connection_authorization_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_pairing_sessions
    ADD CONSTRAINT device_pairing_sessions_creator_device_id_fkey FOREIGN KEY (creator_device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_pairing_sessions
    ADD CONSTRAINT device_pairing_sessions_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_pairing_sessions
    ADD CONSTRAINT device_pairing_sessions_requester_device_id_fkey FOREIGN KEY (requester_device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_pairs
    ADD CONSTRAINT device_pairs_first_device_id_fkey FOREIGN KEY (first_device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_pairs
    ADD CONSTRAINT device_pairs_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_pairs
    ADD CONSTRAINT device_pairs_second_device_id_fkey FOREIGN KEY (second_device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_presence
    ADD CONSTRAINT device_presence_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.device_presence
    ADD CONSTRAINT device_presence_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_comment_audit
    ADD CONSTRAINT figma_comment_audit_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.figma_comment_audit
    ADD CONSTRAINT figma_comment_audit_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.figma_space_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_comment_audit
    ADD CONSTRAINT figma_comment_audit_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_content_records
    ADD CONSTRAINT figma_content_records_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.figma_space_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_content_records
    ADD CONSTRAINT figma_content_records_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_bound_by_user_id_fkey FOREIGN KEY (bound_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.connected_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_shared_resource_id_fkey FOREIGN KEY (shared_resource_id) REFERENCES public.provider_shared_resources(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_space_bindings
    ADD CONSTRAINT figma_space_bindings_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_webhook_deliveries
    ADD CONSTRAINT figma_webhook_deliveries_subscription_id_fkey FOREIGN KEY (subscription_id) REFERENCES public.figma_webhook_subscriptions(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.figma_webhook_subscriptions
    ADD CONSTRAINT figma_webhook_subscriptions_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.figma_space_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_app_installations
    ADD CONSTRAINT github_app_installations_installed_by_user_id_fkey FOREIGN KEY (installed_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.github_app_installations
    ADD CONSTRAINT github_app_installations_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_app_installations
    ADD CONSTRAINT github_app_installations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_app_setup_states
    ADD CONSTRAINT github_app_setup_states_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_app_setup_states
    ADD CONSTRAINT github_app_setup_states_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_bound_by_user_id_fkey FOREIGN KEY (bound_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_installation_id_fkey FOREIGN KEY (installation_id) REFERENCES public.github_app_installations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_shared_resource_id_fkey FOREIGN KEY (shared_resource_id) REFERENCES public.provider_shared_resources(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_code_workspaces
    ADD CONSTRAINT github_code_workspaces_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_credential_handoffs
    ADD CONSTRAINT github_credential_handoffs_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_credential_handoffs
    ADD CONSTRAINT github_credential_handoffs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_credential_handoffs
    ADD CONSTRAINT github_credential_handoffs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.github_code_workspaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_mutation_audit
    ADD CONSTRAINT github_mutation_audit_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.github_mutation_audit
    ADD CONSTRAINT github_mutation_audit_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_mutation_audit
    ADD CONSTRAINT github_mutation_audit_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.github_code_workspaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_repository_records
    ADD CONSTRAINT github_repository_records_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.github_repository_records
    ADD CONSTRAINT github_repository_records_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.github_code_workspaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_blobs
    ADD CONSTRAINT library_blobs_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_derivatives
    ADD CONSTRAINT library_derivatives_derivative_blob_id_fkey FOREIGN KEY (derivative_blob_id) REFERENCES public.library_blobs(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_derivatives
    ADD CONSTRAINT library_derivatives_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_derivatives
    ADD CONSTRAINT library_derivatives_source_file_id_fkey FOREIGN KEY (source_file_id) REFERENCES public.library_files(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_derivatives
    ADD CONSTRAINT library_derivatives_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_exports
    ADD CONSTRAINT library_exports_export_blob_id_fkey FOREIGN KEY (export_blob_id) REFERENCES public.library_blobs(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_exports
    ADD CONSTRAINT library_exports_requested_by_user_id_fkey FOREIGN KEY (requested_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_exports
    ADD CONSTRAINT library_exports_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_exports
    ADD CONSTRAINT library_exports_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_files
    ADD CONSTRAINT library_files_blob_id_fkey FOREIGN KEY (blob_id) REFERENCES public.library_blobs(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_files
    ADD CONSTRAINT library_files_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_files
    ADD CONSTRAINT library_files_uploader_user_id_fkey FOREIGN KEY (uploader_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_item_versions
    ADD CONSTRAINT library_item_versions_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_item_versions
    ADD CONSTRAINT library_item_versions_parent_version_id_fkey FOREIGN KEY (parent_version_id) REFERENCES public.library_item_versions(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_item_versions
    ADD CONSTRAINT library_item_versions_rendition_blob_id_fkey FOREIGN KEY (rendition_blob_id) REFERENCES public.library_blobs(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_item_versions
    ADD CONSTRAINT library_item_versions_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_legal_holds
    ADD CONSTRAINT library_legal_holds_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_legal_holds
    ADD CONSTRAINT library_legal_holds_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_legal_holds
    ADD CONSTRAINT library_legal_holds_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.library_processing_jobs
    ADD CONSTRAINT library_processing_jobs_billing_user_id_fkey FOREIGN KEY (billing_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.library_processing_jobs
    ADD CONSTRAINT library_processing_jobs_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_processing_jobs
    ADD CONSTRAINT library_processing_jobs_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_reauthentication_grants
    ADD CONSTRAINT library_reauthentication_grants_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_reauthentication_grants
    ADD CONSTRAINT library_reauthentication_grants_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.library_recovery_tombstones
    ADD CONSTRAINT library_recovery_tombstones_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.library_recovery_tombstones
    ADD CONSTRAINT library_recovery_tombstones_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.mail_action_audit
    ADD CONSTRAINT mail_action_audit_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.connected_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mail_action_audit
    ADD CONSTRAINT mail_action_audit_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_discovery_snapshots
    ADD CONSTRAINT mcp_discovery_snapshots_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.mcp_remote_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_oauth_credentials
    ADD CONSTRAINT mcp_oauth_credentials_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.mcp_remote_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_oauth_credentials
    ADD CONSTRAINT mcp_oauth_credentials_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_oauth_states
    ADD CONSTRAINT mcp_oauth_states_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_remote_connections
    ADD CONSTRAINT mcp_remote_connections_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_remote_tools
    ADD CONSTRAINT mcp_remote_tools_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.mcp_remote_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_tool_execution_audit
    ADD CONSTRAINT mcp_tool_execution_audit_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_tool_execution_audit
    ADD CONSTRAINT mcp_tool_execution_audit_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.mcp_remote_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.mcp_tool_execution_audit
    ADD CONSTRAINT mcp_tool_execution_audit_connection_id_remote_tool_id_fkey FOREIGN KEY (connection_id, remote_tool_id) REFERENCES public.mcp_remote_tools(connection_id, id);

ALTER TABLE ONLY public.mcp_tool_execution_audit
    ADD CONSTRAINT mcp_tool_execution_audit_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.media_search_assets
    ADD CONSTRAINT media_search_assets_user_id_device_id_fkey FOREIGN KEY (user_id, device_id) REFERENCES public.media_search_devices(user_id, device_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.media_search_assets
    ADD CONSTRAINT media_search_assets_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.media_search_chunks
    ADD CONSTRAINT media_search_chunks_user_id_device_id_asset_id_fkey FOREIGN KEY (user_id, device_id, asset_id) REFERENCES public.media_search_assets(user_id, device_id, asset_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.media_search_devices
    ADD CONSTRAINT media_search_devices_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.media_search_segments
    ADD CONSTRAINT media_search_segments_user_id_device_id_asset_id_chunk_fkey FOREIGN KEY (user_id, device_id, asset_id, chunk_index) REFERENCES public.media_search_chunks(user_id, device_id, asset_id, chunk_index) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_agent_execution_leases
    ADD CONSTRAINT misty_agent_execution_leases_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_agent_execution_leases
    ADD CONSTRAINT misty_agent_execution_leases_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.misty_agent_execution_leases
    ADD CONSTRAINT misty_agent_execution_leases_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_conversations
    ADD CONSTRAINT misty_ask_conversations_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id);

ALTER TABLE ONLY public.misty_ask_conversations
    ADD CONSTRAINT misty_ask_conversations_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.misty_conversation_focus
    ADD CONSTRAINT misty_conversation_focus_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_conversation_focus
    ADD CONSTRAINT misty_conversation_focus_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_conversation_pending_actions
    ADD CONSTRAINT misty_conversation_pending_actions_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_conversation_pending_actions
    ADD CONSTRAINT misty_conversation_pending_actions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_memories
    ADD CONSTRAINT misty_memories_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id);

ALTER TABLE ONLY public.misty_memories
    ADD CONSTRAINT misty_memories_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.misty_memories
    ADD CONSTRAINT misty_memories_source_invocation_id_fkey FOREIGN KEY (source_invocation_id) REFERENCES public.ai_invocations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.misty_memories
    ADD CONSTRAINT misty_memories_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.native_task_effects
    ADD CONSTRAINT native_task_effects_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.native_task_effects
    ADD CONSTRAINT native_task_effects_id_fkey FOREIGN KEY (id) REFERENCES public.space_events(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.native_task_effects
    ADD CONSTRAINT native_task_effects_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.native_task_effects
    ADD CONSTRAINT native_task_effects_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.space_tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.object_deletion_jobs
    ADD CONSTRAINT object_deletion_jobs_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.owner_storage_usage
    ADD CONSTRAINT owner_storage_usage_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.password_recovery_jobs
    ADD CONSTRAINT password_recovery_jobs_issued_user_id_fkey FOREIGN KEY (issued_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.mcp_remote_connections(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_connection_id_remote_tool_id_fkey FOREIGN KEY (connection_id, remote_tool_id) REFERENCES public.mcp_remote_tools(connection_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_mcp_tools
    ADD CONSTRAINT personal_agent_mcp_tools_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.agent_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.space_tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_identity_versions
    ADD CONSTRAINT personal_agent_versions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.misty_ask_identity_versions
    ADD CONSTRAINT personal_agent_versions_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.misty_ask_identities
    ADD CONSTRAINT personal_agents_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.personal_space_templates
    ADD CONSTRAINT personal_space_templates_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_content_records
    ADD CONSTRAINT provider_content_records_shared_resource_id_fkey FOREIGN KEY (shared_resource_id) REFERENCES public.provider_shared_resources(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_content_records
    ADD CONSTRAINT provider_content_records_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_event_inbox
    ADD CONSTRAINT provider_event_inbox_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_event_inbox
    ADD CONSTRAINT provider_event_inbox_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_oauth_states
    ADD CONSTRAINT provider_oauth_states_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_oauth_states
    ADD CONSTRAINT provider_oauth_states_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_shared_resources
    ADD CONSTRAINT provider_shared_resources_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_shared_resources
    ADD CONSTRAINT provider_shared_resources_published_by_user_id_fkey FOREIGN KEY (published_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_shared_resources
    ADD CONSTRAINT provider_shared_resources_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_subscriptions
    ADD CONSTRAINT provider_subscriptions_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_subscriptions
    ADD CONSTRAINT provider_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.realtime_tickets
    ADD CONSTRAINT realtime_tickets_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_backend_connection_versions
    ADD CONSTRAINT sdk_backend_connection_versions_user_id_id_fkey FOREIGN KEY (user_id, id) REFERENCES public.sdk_backend_connections(user_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_backend_connections
    ADD CONSTRAINT sdk_backend_connections_account_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_capability_contract_versions
    ADD CONSTRAINT sdk_capability_contract_versions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_capability_invocations
    ADD CONSTRAINT sdk_capability_invocations_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_capability_invocations
    ADD CONSTRAINT sdk_capability_invocations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_capability_invocations
    ADD CONSTRAINT sdk_capability_invocations_user_id_target_id_target_revisi_fkey FOREIGN KEY (user_id, target_id, target_revision) REFERENCES public.sdk_target_versions(user_id, id, revision);

ALTER TABLE ONLY public.sdk_provider_registrations
    ADD CONSTRAINT sdk_provider_registrations_account_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_provider_registrations
    ADD CONSTRAINT sdk_provider_registrations_user_id_provider_id_version_fkey FOREIGN KEY (user_id, provider_id, version) REFERENCES public.sdk_provider_versions(user_id, provider_id, version);

ALTER TABLE ONLY public.sdk_target_versions
    ADD CONSTRAINT sdk_target_versions_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_target_versions
    ADD CONSTRAINT sdk_target_versions_user_id_connection_id_connection_revis_fkey FOREIGN KEY (user_id, connection_id, connection_revision) REFERENCES public.sdk_backend_connection_versions(user_id, id, revision);

ALTER TABLE ONLY public.sdk_target_versions
    ADD CONSTRAINT sdk_target_versions_user_id_id_fkey FOREIGN KEY (user_id, id) REFERENCES public.sdk_targets(user_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sdk_target_versions
    ADD CONSTRAINT sdk_target_versions_user_id_provider_id_provider_version_fkey FOREIGN KEY (user_id, provider_id, provider_version) REFERENCES public.sdk_provider_versions(user_id, provider_id, version);

ALTER TABLE ONLY public.sdk_targets
    ADD CONSTRAINT sdk_targets_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.security_domains
    ADD CONSTRAINT security_domains_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.security_domains
    ADD CONSTRAINT security_domains_space_fk FOREIGN KEY (space_id) REFERENCES public.spaces(id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.self_host_accounts
    ADD CONSTRAINT self_host_accounts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.self_host_enrollment_invitations
    ADD CONSTRAINT self_host_enrollment_invitations_consumed_by_fkey FOREIGN KEY (consumed_by) REFERENCES public.self_host_accounts(user_id) ON DELETE SET NULL;

ALTER TABLE ONLY public.self_host_enrollment_invitations
    ADD CONSTRAINT self_host_enrollment_invitations_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.self_host_accounts(user_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_assets
    ADD CONSTRAINT smart_library_assets_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.smart_library_folders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_assets
    ADD CONSTRAINT smart_library_assets_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_batches
    ADD CONSTRAINT smart_library_batches_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.smart_library_folders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_cost_events
    ADD CONSTRAINT smart_library_cost_events_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.smart_library_folders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_cost_events
    ADD CONSTRAINT smart_library_cost_events_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_folders
    ADD CONSTRAINT smart_library_folders_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_reindex_jobs
    ADD CONSTRAINT smart_library_reindex_jobs_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.smart_library_folders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.smart_library_reindex_jobs
    ADD CONSTRAINT smart_library_reindex_jobs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_rules
    ADD CONSTRAINT social_automation_rules_authority_id_fkey FOREIGN KEY (authority_id) REFERENCES public.social_send_authorities(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.social_automation_rules
    ADD CONSTRAINT social_automation_rules_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.social_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_rules
    ADD CONSTRAINT social_automation_rules_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_rules
    ADD CONSTRAINT social_automation_rules_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_rules
    ADD CONSTRAINT social_automation_rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_runs
    ADD CONSTRAINT social_automation_runs_outbound_command_id_fkey FOREIGN KEY (outbound_command_id) REFERENCES public.social_outbound_commands(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.social_automation_runs
    ADD CONSTRAINT social_automation_runs_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.social_automation_rules(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_runs
    ADD CONSTRAINT social_automation_runs_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_automation_runs
    ADD CONSTRAINT social_automation_runs_trigger_message_id_fkey FOREIGN KEY (trigger_message_id) REFERENCES public.space_messages(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.social_bindings
    ADD CONSTRAINT social_bindings_connected_by_user_id_fkey FOREIGN KEY (connected_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_bindings
    ADD CONSTRAINT social_bindings_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.connected_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_bindings
    ADD CONSTRAINT social_bindings_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.social_bindings
    ADD CONSTRAINT social_bindings_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_identities
    ADD CONSTRAINT social_identities_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.social_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_authority_id_fkey FOREIGN KEY (authority_id) REFERENCES public.social_send_authorities(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.social_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_requested_by_user_id_fkey FOREIGN KEY (requested_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.social_outbound_commands
    ADD CONSTRAINT social_outbound_commands_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_authority_id_fkey FOREIGN KEY (authority_id) REFERENCES public.social_send_authorities(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.social_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_command_fk FOREIGN KEY (outbound_command_id) REFERENCES public.social_outbound_commands(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_scheduled_messages
    ADD CONSTRAINT social_scheduled_messages_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_send_authorities
    ADD CONSTRAINT social_send_authorities_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.social_bindings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_send_authorities
    ADD CONSTRAINT social_send_authorities_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.connected_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_send_authorities
    ADD CONSTRAINT social_send_authorities_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.social_send_authorities
    ADD CONSTRAINT social_send_authorities_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_album_folders
    ADD CONSTRAINT space_album_folders_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_album_folders
    ADD CONSTRAINT space_album_folders_parent_folder_id_fkey FOREIGN KEY (parent_folder_id) REFERENCES public.space_album_folders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_album_folders
    ADD CONSTRAINT space_album_folders_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_album_items
    ADD CONSTRAINT space_album_items_added_by_user_id_fkey FOREIGN KEY (added_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_album_items
    ADD CONSTRAINT space_album_items_album_id_fkey FOREIGN KEY (album_id) REFERENCES public.space_albums(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_album_items
    ADD CONSTRAINT space_album_items_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_albums
    ADD CONSTRAINT space_albums_cover_item_id_fkey FOREIGN KEY (cover_item_id) REFERENCES public.space_library_items(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_albums
    ADD CONSTRAINT space_albums_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_albums
    ADD CONSTRAINT space_albums_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.space_album_folders(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_albums
    ADD CONSTRAINT space_albums_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_calendar_events
    ADD CONSTRAINT space_calendar_events_source_id_fkey FOREIGN KEY (source_id) REFERENCES public.space_calendar_sources(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_calendar_events
    ADD CONSTRAINT space_calendar_events_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_calendar_sources
    ADD CONSTRAINT space_calendar_sources_connected_by_user_id_fkey FOREIGN KEY (connected_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_calendar_sources
    ADD CONSTRAINT space_calendar_sources_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_calendar_sources
    ADD CONSTRAINT space_calendar_sources_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversation_members
    ADD CONSTRAINT space_conversation_members_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversation_members
    ADD CONSTRAINT space_conversation_members_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversation_members
    ADD CONSTRAINT space_conversation_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversation_reads
    ADD CONSTRAINT space_conversation_reads_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversation_reads
    ADD CONSTRAINT space_conversation_reads_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversations
    ADD CONSTRAINT space_conversations_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversations
    ADD CONSTRAINT space_conversations_direct_agent_id_fkey FOREIGN KEY (direct_agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversations
    ADD CONSTRAINT space_conversations_direct_user_id_fkey FOREIGN KEY (direct_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_conversations
    ADD CONSTRAINT space_conversations_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_conversations
    ADD CONSTRAINT space_conversations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_creation_requests
    ADD CONSTRAINT space_creation_requests_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_creation_requests
    ADD CONSTRAINT space_creation_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_device_presence
    ADD CONSTRAINT space_device_presence_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_device_presence
    ADD CONSTRAINT space_device_presence_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_discord_links
    ADD CONSTRAINT space_discord_links_connected_by_user_id_fkey FOREIGN KEY (connected_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_discord_links
    ADD CONSTRAINT space_discord_links_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_discord_links
    ADD CONSTRAINT space_discord_links_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_discord_links
    ADD CONSTRAINT space_discord_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_drawing_assets
    ADD CONSTRAINT space_drawing_assets_drawing_id_fkey FOREIGN KEY (drawing_id) REFERENCES public.space_drawings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_drawing_assets
    ADD CONSTRAINT space_drawing_assets_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.library_files(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_drawing_assets
    ADD CONSTRAINT space_drawing_assets_uploader_user_id_fkey FOREIGN KEY (uploader_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_drawing_control_outbox
    ADD CONSTRAINT space_drawing_control_outbox_drawing_id_fkey FOREIGN KEY (drawing_id) REFERENCES public.space_drawings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_drawings
    ADD CONSTRAINT space_drawings_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_drawings
    ADD CONSTRAINT space_drawings_creator_user_id_fkey FOREIGN KEY (creator_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_drawings
    ADD CONSTRAINT space_drawings_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_events
    ADD CONSTRAINT space_events_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_events
    ADD CONSTRAINT space_events_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_inbox_items
    ADD CONSTRAINT space_inbox_items_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.space_events(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_inbox_items
    ADD CONSTRAINT space_inbox_items_message_id_fkey FOREIGN KEY (message_id) REFERENCES public.space_messages(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_inbox_items
    ADD CONSTRAINT space_inbox_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_inbox_items
    ADD CONSTRAINT space_inbox_items_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_integrations
    ADD CONSTRAINT space_integrations_connected_by_user_id_fkey FOREIGN KEY (connected_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_integrations
    ADD CONSTRAINT space_integrations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_invitation_delivery_jobs
    ADD CONSTRAINT space_invitation_delivery_jobs_invite_id_fkey FOREIGN KEY (invite_id) REFERENCES public.space_invitations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_invitations
    ADD CONSTRAINT space_invitations_invited_by_user_id_fkey FOREIGN KEY (invited_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_invitations
    ADD CONSTRAINT space_invitations_invited_user_id_fkey FOREIGN KEY (invited_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_invitations
    ADD CONSTRAINT space_invitations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_item_aliases
    ADD CONSTRAINT space_item_aliases_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_item_aliases
    ADD CONSTRAINT space_item_aliases_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_asset_stack_members
    ADD CONSTRAINT space_library_asset_stack_members_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_asset_stack_members
    ADD CONSTRAINT space_library_asset_stack_members_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES public.space_library_asset_stacks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_asset_stacks
    ADD CONSTRAINT space_library_asset_stacks_cover_item_id_fkey FOREIGN KEY (cover_item_id) REFERENCES public.space_library_items(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_asset_stacks
    ADD CONSTRAINT space_library_asset_stacks_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_asset_stacks
    ADD CONSTRAINT space_library_asset_stacks_motion_item_id_fkey FOREIGN KEY (motion_item_id) REFERENCES public.space_library_items(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_library_asset_stacks
    ADD CONSTRAINT space_library_asset_stacks_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_audit_events
    ADD CONSTRAINT space_library_audit_events_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_library_audit_events
    ADD CONSTRAINT space_library_audit_events_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_library_audit_events
    ADD CONSTRAINT space_library_audit_events_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_library_direct_references
    ADD CONSTRAINT space_library_direct_references_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_direct_references
    ADD CONSTRAINT space_library_direct_references_destination_space_id_fkey FOREIGN KEY (destination_space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_direct_references
    ADD CONSTRAINT space_library_direct_references_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES public.space_library_grants(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_grants
    ADD CONSTRAINT space_library_grants_destination_space_id_fkey FOREIGN KEY (destination_space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_grants
    ADD CONSTRAINT space_library_grants_granted_by_user_id_fkey FOREIGN KEY (granted_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_grants
    ADD CONSTRAINT space_library_grants_source_item_id_fkey FOREIGN KEY (source_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_grants
    ADD CONSTRAINT space_library_grants_source_space_id_fkey FOREIGN KEY (source_space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_groups
    ADD CONSTRAINT space_library_groups_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_groups
    ADD CONSTRAINT space_library_groups_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_destination_item_id_fkey FOREIGN KEY (destination_item_id) REFERENCES public.space_library_items(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_destination_security_domain_id_fkey FOREIGN KEY (destination_security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_destination_space_id_fkey FOREIGN KEY (destination_space_id) REFERENCES public.spaces(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_importer_user_id_fkey FOREIGN KEY (importer_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_quota_reservation_upload_id_fkey FOREIGN KEY (quota_reservation_upload_id) REFERENCES public.space_library_uploads(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_source_item_id_fkey FOREIGN KEY (source_item_id) REFERENCES public.space_library_items(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_source_security_domain_id_fkey FOREIGN KEY (source_security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_imports
    ADD CONSTRAINT space_library_imports_source_space_id_fkey FOREIGN KEY (source_space_id) REFERENCES public.spaces(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_intelligence_policies
    ADD CONSTRAINT space_library_intelligence_policies_enabled_by_user_id_fkey FOREIGN KEY (enabled_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_library_intelligence_policies
    ADD CONSTRAINT space_library_intelligence_policies_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_item_views
    ADD CONSTRAINT space_library_item_views_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_item_views
    ADD CONSTRAINT space_library_item_views_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_item_views
    ADD CONSTRAINT space_library_item_views_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_added_by_user_id_fkey FOREIGN KEY (added_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_contributing_user_id_fkey FOREIGN KEY (contributing_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_current_version_fk FOREIGN KEY (current_edit_version_id) REFERENCES public.library_item_versions(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.library_files(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_items
    ADD CONSTRAINT space_library_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_search_documents
    ADD CONSTRAINT space_library_search_documents_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_search_documents
    ADD CONSTRAINT space_library_search_documents_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_search_documents
    ADD CONSTRAINT space_library_search_documents_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_drawing_id_fkey FOREIGN KEY (drawing_id) REFERENCES public.space_drawings(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.library_files(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_note_id_fkey FOREIGN KEY (note_id) REFERENCES public.space_notes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_security_domain_id_fkey FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_library_uploads
    ADD CONSTRAINT space_library_uploads_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_member_permission_overrides
    ADD CONSTRAINT space_member_permission_overrides_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_member_permission_overrides
    ADD CONSTRAINT space_member_permission_overrides_updated_by_user_id_fkey FOREIGN KEY (updated_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_member_permission_overrides
    ADD CONSTRAINT space_member_permission_overrides_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_member_roles
    ADD CONSTRAINT space_member_roles_assigned_by_user_id_fkey FOREIGN KEY (assigned_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_member_roles
    ADD CONSTRAINT space_member_roles_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.space_roles(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_member_roles
    ADD CONSTRAINT space_member_roles_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_member_roles
    ADD CONSTRAINT space_member_roles_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_member_storage_usage
    ADD CONSTRAINT space_member_storage_usage_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_member_storage_usage
    ADD CONSTRAINT space_member_storage_usage_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_members
    ADD CONSTRAINT space_members_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_members
    ADD CONSTRAINT space_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_memory_preferences
    ADD CONSTRAINT space_memory_preferences_cover_item_id_fkey FOREIGN KEY (cover_item_id) REFERENCES public.space_library_items(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_memory_preferences
    ADD CONSTRAINT space_memory_preferences_music_item_id_fkey FOREIGN KEY (music_item_id) REFERENCES public.space_library_items(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_memory_preferences
    ADD CONSTRAINT space_memory_preferences_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_memory_preferences
    ADD CONSTRAINT space_memory_preferences_updated_by_user_id_fkey FOREIGN KEY (updated_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.library_files(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_message_id_fkey FOREIGN KEY (message_id) REFERENCES public.space_messages(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_promoted_item_id_fkey FOREIGN KEY (promoted_item_id) REFERENCES public.space_library_items(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_upload_id_fkey FOREIGN KEY (upload_id) REFERENCES public.space_library_uploads(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_message_attachments
    ADD CONSTRAINT space_message_attachments_uploader_user_id_fkey FOREIGN KEY (uploader_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_message_library_references
    ADD CONSTRAINT space_message_library_references_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_message_library_references
    ADD CONSTRAINT space_message_library_references_message_id_fkey FOREIGN KEY (message_id) REFERENCES public.space_messages(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_message_library_references
    ADD CONSTRAINT space_message_library_references_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_message_library_references
    ADD CONSTRAINT space_message_library_references_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_message_reactions
    ADD CONSTRAINT space_message_reactions_message_id_fkey FOREIGN KEY (message_id) REFERENCES public.space_messages(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_message_reactions
    ADD CONSTRAINT space_message_reactions_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_message_reactions
    ADD CONSTRAINT space_message_reactions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_reply_to_message_id_fkey FOREIGN KEY (reply_to_message_id) REFERENCES public.space_messages(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_sender_user_id_fkey FOREIGN KEY (sender_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_social_identity_id_fkey FOREIGN KEY (social_identity_id) REFERENCES public.social_identities(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_messages
    ADD CONSTRAINT space_messages_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_native_calendar_events
    ADD CONSTRAINT space_native_calendar_events_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_native_calendar_events
    ADD CONSTRAINT space_native_calendar_events_created_by_agent_id_fkey FOREIGN KEY (created_by_agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_native_calendar_events
    ADD CONSTRAINT space_native_calendar_events_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_native_calendar_events
    ADD CONSTRAINT space_native_calendar_events_source_run_id_fkey FOREIGN KEY (source_run_id) REFERENCES public.space_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_native_calendar_events
    ADD CONSTRAINT space_native_calendar_events_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_nodes
    ADD CONSTRAINT space_nodes_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.space_nodes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_nodes
    ADD CONSTRAINT space_nodes_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_nodes
    ADD CONSTRAINT space_nodes_uploader_user_id_fkey FOREIGN KEY (uploader_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_assets
    ADD CONSTRAINT space_note_assets_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.library_files(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_note_assets
    ADD CONSTRAINT space_note_assets_note_id_fkey FOREIGN KEY (note_id) REFERENCES public.space_notes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_assets
    ADD CONSTRAINT space_note_assets_uploader_user_id_fkey FOREIGN KEY (uploader_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_note_control_outbox
    ADD CONSTRAINT space_note_control_outbox_note_id_fkey FOREIGN KEY (note_id) REFERENCES public.space_notes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_links
    ADD CONSTRAINT space_note_links_source_note_id_fkey FOREIGN KEY (source_note_id) REFERENCES public.space_notes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_links
    ADD CONSTRAINT space_note_links_target_note_id_fkey FOREIGN KEY (target_note_id) REFERENCES public.space_notes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_permissions
    ADD CONSTRAINT space_note_permissions_granted_by_fkey FOREIGN KEY (granted_by) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_permissions
    ADD CONSTRAINT space_note_permissions_note_id_fkey FOREIGN KEY (note_id) REFERENCES public.space_notes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_note_permissions
    ADD CONSTRAINT space_note_permissions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_notes
    ADD CONSTRAINT space_notes_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_notes
    ADD CONSTRAINT space_notes_creator_user_id_fkey FOREIGN KEY (creator_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_notes
    ADD CONSTRAINT space_notes_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_people
    ADD CONSTRAINT space_people_cover_item_id_fkey FOREIGN KEY (cover_item_id) REFERENCES public.space_library_items(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_people
    ADD CONSTRAINT space_people_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_people
    ADD CONSTRAINT space_people_merged_into_id_fkey FOREIGN KEY (merged_into_id) REFERENCES public.space_people(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_people
    ADD CONSTRAINT space_people_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_person_observations
    ADD CONSTRAINT space_person_observations_derivative_id_fkey FOREIGN KEY (derivative_id) REFERENCES public.library_derivatives(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_person_observations
    ADD CONSTRAINT space_person_observations_person_id_fkey FOREIGN KEY (person_id) REFERENCES public.space_people(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_person_observations
    ADD CONSTRAINT space_person_observations_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES public.space_library_items(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_pinned_collections
    ADD CONSTRAINT space_pinned_collections_pinned_by_user_id_fkey FOREIGN KEY (pinned_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_pinned_collections
    ADD CONSTRAINT space_pinned_collections_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_provider_credentials
    ADD CONSTRAINT space_provider_credentials_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_provider_credentials
    ADD CONSTRAINT space_provider_credentials_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_provider_credentials
    ADD CONSTRAINT space_provider_credentials_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_rendition_reservations
    ADD CONSTRAINT space_rendition_reservations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_rendition_reservations
    ADD CONSTRAINT space_rendition_reservations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_resolve_tickets
    ADD CONSTRAINT space_resolve_tickets_node_id_fkey FOREIGN KEY (node_id) REFERENCES public.space_nodes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_resolve_tickets
    ADD CONSTRAINT space_resolve_tickets_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_resolve_tickets
    ADD CONSTRAINT space_resolve_tickets_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_edges
    ADD CONSTRAINT space_roadmap_edges_roadmap_id_space_id_fkey FOREIGN KEY (roadmap_id, space_id) REFERENCES public.space_roadmaps(id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_edges
    ADD CONSTRAINT space_roadmap_edges_source_goal_id_roadmap_id_space_id_fkey FOREIGN KEY (source_goal_id, roadmap_id, space_id) REFERENCES public.space_roadmap_goals(id, roadmap_id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_edges
    ADD CONSTRAINT space_roadmap_edges_target_goal_id_roadmap_id_space_id_fkey FOREIGN KEY (target_goal_id, roadmap_id, space_id) REFERENCES public.space_roadmap_goals(id, roadmap_id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_goal_tasks
    ADD CONSTRAINT space_roadmap_goal_tasks_added_by_user_id_fkey FOREIGN KEY (added_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_roadmap_goal_tasks
    ADD CONSTRAINT space_roadmap_goal_tasks_goal_id_roadmap_id_space_id_fkey FOREIGN KEY (goal_id, roadmap_id, space_id) REFERENCES public.space_roadmap_goals(id, roadmap_id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_goal_tasks
    ADD CONSTRAINT space_roadmap_goal_tasks_task_id_space_id_fkey FOREIGN KEY (task_id, space_id) REFERENCES public.space_tasks(id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_goals
    ADD CONSTRAINT space_roadmap_goals_manual_completed_by_user_id_fkey FOREIGN KEY (manual_completed_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_roadmap_goals
    ADD CONSTRAINT space_roadmap_goals_milestone_id_roadmap_id_space_id_fkey FOREIGN KEY (milestone_id, roadmap_id, space_id) REFERENCES public.space_roadmap_milestones(id, roadmap_id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_goals
    ADD CONSTRAINT space_roadmap_goals_roadmap_id_space_id_fkey FOREIGN KEY (roadmap_id, space_id) REFERENCES public.space_roadmaps(id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_milestones
    ADD CONSTRAINT space_roadmap_milestones_roadmap_id_space_id_fkey FOREIGN KEY (roadmap_id, space_id) REFERENCES public.space_roadmaps(id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_node_definitions
    ADD CONSTRAINT space_roadmap_node_definitions_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_roadmap_node_definitions
    ADD CONSTRAINT space_roadmap_node_definitions_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_nodes
    ADD CONSTRAINT space_roadmap_nodes_definition_id_space_id_fkey FOREIGN KEY (definition_id, space_id) REFERENCES public.space_roadmap_node_definitions(id, space_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_roadmap_nodes
    ADD CONSTRAINT space_roadmap_nodes_milestone_id_roadmap_id_space_id_fkey FOREIGN KEY (milestone_id, roadmap_id, space_id) REFERENCES public.space_roadmap_milestones(id, roadmap_id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmap_nodes
    ADD CONSTRAINT space_roadmap_nodes_roadmap_id_space_id_fkey FOREIGN KEY (roadmap_id, space_id) REFERENCES public.space_roadmaps(id, space_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmaps
    ADD CONSTRAINT space_roadmaps_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roadmaps
    ADD CONSTRAINT space_roadmaps_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_roadmaps
    ADD CONSTRAINT space_roadmaps_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_roles
    ADD CONSTRAINT space_roles_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_run_actions
    ADD CONSTRAINT space_run_actions_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_run_approvals
    ADD CONSTRAINT space_run_approvals_decided_by_user_id_fkey FOREIGN KEY (decided_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_run_approvals
    ADD CONSTRAINT space_run_approvals_requested_from_user_id_fkey FOREIGN KEY (requested_from_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_run_approvals
    ADD CONSTRAINT space_run_approvals_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_run_steps
    ADD CONSTRAINT space_run_steps_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_billing_user_id_fkey FOREIGN KEY (billing_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_content_space_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_initiated_by_user_id_fkey FOREIGN KEY (initiated_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_parent_run_id_fkey FOREIGN KEY (parent_run_id) REFERENCES public.space_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_requesting_member_id_fkey FOREIGN KEY (requesting_member_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_retry_of_run_id_fkey FOREIGN KEY (retry_of_run_id) REFERENCES public.space_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_scope_conversation_id_fkey FOREIGN KEY (scope_conversation_id) REFERENCES public.space_conversations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_source_agent_conversation_id_fkey FOREIGN KEY (source_agent_conversation_id) REFERENCES public.misty_ask_conversations(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_source_message_id_fkey FOREIGN KEY (source_message_id) REFERENCES public.space_messages(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_source_task_id_fkey FOREIGN KEY (source_task_id) REFERENCES public.space_tasks(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_runs
    ADD CONSTRAINT space_runs_workflow_version_id_fkey FOREIGN KEY (workflow_version_id) REFERENCES public.space_workflow_versions(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_setup_integrations
    ADD CONSTRAINT space_setup_integrations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_connected_by_user_id_fkey FOREIGN KEY (connected_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES public.space_integrations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_shared_resource_id_fkey FOREIGN KEY (shared_resource_id) REFERENCES public.provider_shared_resources(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_slack_links
    ADD CONSTRAINT space_slack_links_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.library_files(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_storage_contributions
    ADD CONSTRAINT space_storage_contributions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_storage_usage
    ADD CONSTRAINT space_storage_usage_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_task_activity
    ADD CONSTRAINT space_task_activity_actor_agent_id_fkey FOREIGN KEY (actor_agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_task_activity
    ADD CONSTRAINT space_task_activity_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_task_activity
    ADD CONSTRAINT space_task_activity_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_task_activity
    ADD CONSTRAINT space_task_activity_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_task_activity
    ADD CONSTRAINT space_task_activity_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.space_tasks(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_task_counters
    ADD CONSTRAINT space_task_counters_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_assignee_agent_id_fkey FOREIGN KEY (assignee_agent_id) REFERENCES public.misty_ask_identities(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_assignee_user_id_fkey FOREIGN KEY (assignee_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES public.space_conversations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_audience_creator_user_id_fkey FOREIGN KEY (audience_creator_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_source_run_id_fkey FOREIGN KEY (source_run_id) REFERENCES public.space_runs(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.space_tasks
    ADD CONSTRAINT space_tasks_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_upload_reservations
    ADD CONSTRAINT space_upload_reservations_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_upload_reservations
    ADD CONSTRAINT space_upload_reservations_upload_id_fkey FOREIGN KEY (upload_id) REFERENCES public.space_library_uploads(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_upload_reservations
    ADD CONSTRAINT space_upload_reservations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_workflow_action_journal
    ADD CONSTRAINT space_workflow_action_journal_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_workflow_resource_leases
    ADD CONSTRAINT space_workflow_resource_leases_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_workflow_versions
    ADD CONSTRAINT space_workflow_versions_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.space_workflow_versions
    ADD CONSTRAINT space_workflow_versions_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_workflow_versions
    ADD CONSTRAINT space_workflow_versions_workflow_id_fkey FOREIGN KEY (workflow_id) REFERENCES public.space_workflows(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_workflows
    ADD CONSTRAINT space_workflows_creator_user_id_fkey FOREIGN KEY (creator_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.space_workflows
    ADD CONSTRAINT space_workflows_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.spaces
    ADD CONSTRAINT spaces_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.spaces
    ADD CONSTRAINT spaces_security_domain_fk FOREIGN KEY (security_domain_id) REFERENCES public.security_domains(id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.licenses
    ADD CONSTRAINT subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.trusted_device_request_nonces
    ADD CONSTRAINT trusted_device_request_nonces_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.trusted_devices(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.trusted_device_request_nonces
    ADD CONSTRAINT trusted_device_request_nonces_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.trusted_devices
    ADD CONSTRAINT trusted_devices_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_app_activity
    ADD CONSTRAINT user_app_activity_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_global_home_activity
    ADD CONSTRAINT user_global_home_activity_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_home_activity
    ADD CONSTRAINT user_home_activity_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_home_activity
    ADD CONSTRAINT user_home_activity_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_license_id_fkey FOREIGN KEY (license_id) REFERENCES public.licenses(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_ai_context_id_fkey FOREIGN KEY (ai_context_id) REFERENCES public.ai_invocation_contexts(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_assigned_device_id_fkey FOREIGN KEY (assigned_device_id) REFERENCES public.trusted_devices(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_context_id_fkey FOREIGN KEY (context_id) REFERENCES public.agent_run_contexts(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_invocation_id_fkey FOREIGN KEY (invocation_id) REFERENCES public.ai_invocations(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.space_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.workflow_device_node_jobs
    ADD CONSTRAINT workflow_device_node_jobs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE public.abuse_blocks ENABLE ROW LEVEL SECURITY;

CREATE POLICY abuse_blocks_service_policy ON public.abuse_blocks USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.account_deletion_provider_resources ENABLE ROW LEVEL SECURITY;

CREATE POLICY account_deletion_provider_resources_service ON public.account_deletion_provider_resources USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.account_deletion_requests ENABLE ROW LEVEL SECURITY;

CREATE POLICY account_deletion_requests_service ON public.account_deletion_requests USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.account_deletion_steps ENABLE ROW LEVEL SECURITY;

CREATE POLICY account_deletion_steps_service ON public.account_deletion_steps USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY agent_conversation_events_policy ON public.misty_ask_conversation_events USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY agent_conversations_policy ON public.misty_ask_conversations USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.agent_model_turn_claims ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.agent_run_contexts ENABLE ROW LEVEL SECURITY;

CREATE POLICY agent_run_contexts_owner_policy ON public.agent_run_contexts USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.agent_run_jobs ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.agent_run_tool_approvals ENABLE ROW LEVEL SECURITY;

CREATE POLICY agent_run_tool_approvals_owner_policy ON public.agent_run_tool_approvals USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.agent_runtime_deliveries ENABLE ROW LEVEL SECURITY;

CREATE POLICY agent_runtime_deliveries_service ON public.agent_runtime_deliveries USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.agent_runtime_start_receipts ENABLE ROW LEVEL SECURITY;

CREATE POLICY agent_runtime_start_receipts_service ON public.agent_runtime_start_receipts USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.agent_sdk_capability_bindings ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.agent_toolbox_action_journal ENABLE ROW LEVEL SECURITY;

CREATE POLICY agent_toolbox_action_journal_private ON public.agent_toolbox_action_journal USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_artifacts ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_artifacts_owner_policy ON public.ai_artifacts USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_cleanup_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_cleanup_jobs_owner_insert_policy ON public.ai_cleanup_jobs FOR INSERT WITH CHECK ((user_id = public.misty_rls_user_id()));

CREATE POLICY ai_cleanup_jobs_owner_policy ON public.ai_cleanup_jobs FOR SELECT USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY ai_cleanup_jobs_service_write_policy ON public.ai_cleanup_jobs USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.ai_conversation_attachments ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_conversation_attachments_policy ON public.ai_conversation_attachments USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_feature_flags ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_feature_flags_read_policy ON public.ai_feature_flags FOR SELECT USING (true);

CREATE POLICY ai_feature_flags_service_write_policy ON public.ai_feature_flags USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.ai_feedback ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_feedback_owner_policy ON public.ai_feedback USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_intervention_waits ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_intervention_waits_service ON public.ai_intervention_waits USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.ai_invocation_contexts ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_invocation_contexts_owner_policy ON public.ai_invocation_contexts USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_invocation_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_invocation_events_owner_policy ON public.ai_invocation_events USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.ai_invocations i
  WHERE ((i.id = ai_invocation_events.invocation_id) AND (i.user_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.ai_invocations i
  WHERE ((i.id = ai_invocation_events.invocation_id) AND (i.user_id = public.misty_rls_user_id()))))));

ALTER TABLE public.ai_invocations ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_invocations_owner_policy ON public.ai_invocations USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_recaps ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_recaps_owner_policy ON public.ai_recaps USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_retrieval_chunks ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_retrieval_chunks_read_policy ON public.ai_retrieval_chunks FOR SELECT USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.ai_retrieval_documents d
  WHERE (d.id = ai_retrieval_chunks.document_id)))));

CREATE POLICY ai_retrieval_chunks_service_write_policy ON public.ai_retrieval_chunks USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.ai_retrieval_documents ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_retrieval_documents_private_delete_policy ON public.ai_retrieval_documents FOR DELETE USING (((owner_user_id = public.misty_rls_user_id()) AND (privacy_class = 'private'::text)));

CREATE POLICY ai_retrieval_documents_read_policy ON public.ai_retrieval_documents FOR SELECT USING ((public.misty_rls_is_service() OR ((lifecycle_state = 'active'::text) AND (((privacy_class = 'private'::text) AND (owner_user_id = public.misty_rls_user_id())) OR ((privacy_class = 'shared'::text) AND public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)) OR ((privacy_class = 'provider'::text) AND public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id) AND (((source_kind = 'provider'::text) AND (EXISTS ( SELECT 1
   FROM (public.provider_content_records p
     JOIN public.provider_shared_resources r ON ((r.id = p.shared_resource_id)))
  WHERE ((p.id = ai_retrieval_documents.source_id) AND (p.deleted_at IS NULL) AND (r.status = 'active'::text))))) OR ((source_kind = 'calendar'::text) AND (EXISTS ( SELECT 1
   FROM (public.space_calendar_events e
     JOIN public.space_calendar_sources s ON ((s.id = e.source_id)))
  WHERE ((e.id = e.source_id) AND (e.removed_at IS NULL) AND (s.status = 'active'::text)))))))))));

CREATE POLICY ai_retrieval_documents_service_write_policy ON public.ai_retrieval_documents USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.ai_runtime_callback_receipts ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_runtime_callback_receipts_service ON public.ai_runtime_callback_receipts USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.ai_surface_preferences ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_surface_preferences_owner_policy ON public.ai_surface_preferences USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.ai_user_settings ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_user_settings_owner_policy ON public.ai_user_settings USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.auth_handoff_tokens ENABLE ROW LEVEL SECURITY;

CREATE POLICY auth_handoff_tokens_all_policy ON public.auth_handoff_tokens USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.cloud_connections ENABLE ROW LEVEL SECURITY;

CREATE POLICY cloud_connections_owner ON public.cloud_connections USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.cloud_credential_handoffs ENABLE ROW LEVEL SECURITY;

CREATE POLICY cloud_credential_handoffs_owner ON public.cloud_credential_handoffs USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.cloud_oauth_states ENABLE ROW LEVEL SECURITY;

CREATE POLICY cloud_oauth_states_owner ON public.cloud_oauth_states USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.connected_account_oauth_states ENABLE ROW LEVEL SECURITY;

CREATE POLICY connected_account_oauth_states_owner ON public.connected_account_oauth_states USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.connected_accounts ENABLE ROW LEVEL SECURITY;

CREATE POLICY connected_accounts_owner ON public.connected_accounts USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.connection_authorization_requests ENABLE ROW LEVEL SECURITY;

CREATE POLICY connection_authorization_requests_owner ON public.connection_authorization_requests USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.device_pairing_sessions ENABLE ROW LEVEL SECURITY;

CREATE POLICY device_pairing_sessions_owner_policy ON public.device_pairing_sessions USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.device_pairs ENABLE ROW LEVEL SECURITY;

CREATE POLICY device_pairs_owner_policy ON public.device_pairs USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.device_presence ENABLE ROW LEVEL SECURITY;

CREATE POLICY device_presence_owner_policy ON public.device_presence USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

CREATE POLICY direct_references_policy ON public.space_library_direct_references USING ((public.misty_rls_is_service() OR public.misty_is_space_member(destination_space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(destination_space_id)));

ALTER TABLE public.figma_comment_audit ENABLE ROW LEVEL SECURITY;

CREATE POLICY figma_comment_audit_member_read ON public.figma_comment_audit FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY figma_comment_audit_service_write ON public.figma_comment_audit USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.figma_content_records ENABLE ROW LEVEL SECURITY;

CREATE POLICY figma_content_records_member_read ON public.figma_content_records FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY figma_content_records_service_write ON public.figma_content_records USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.figma_space_bindings ENABLE ROW LEVEL SECURITY;

CREATE POLICY figma_space_bindings_member_read ON public.figma_space_bindings FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY figma_space_bindings_service_write ON public.figma_space_bindings USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.figma_webhook_deliveries ENABLE ROW LEVEL SECURITY;

CREATE POLICY figma_webhook_deliveries_service ON public.figma_webhook_deliveries USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.figma_webhook_subscriptions ENABLE ROW LEVEL SECURITY;

CREATE POLICY figma_webhook_subscriptions_member_read ON public.figma_webhook_subscriptions FOR SELECT USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.figma_space_bindings binding
  WHERE ((binding.id = figma_webhook_subscriptions.binding_id) AND public.misty_is_space_member(binding.space_id))))));

CREATE POLICY figma_webhook_subscriptions_service_write ON public.figma_webhook_subscriptions USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.github_app_installations ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_app_installations_member ON public.github_app_installations USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.github_app_setup_states ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_app_setup_states_owner ON public.github_app_setup_states USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.github_code_workspaces ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_code_workspaces_member ON public.github_code_workspaces USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.github_credential_handoffs ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_credential_handoffs_owner ON public.github_credential_handoffs USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.github_mutation_audit ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_mutation_audit_member ON public.github_mutation_audit FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY github_mutation_audit_service_write ON public.github_mutation_audit FOR INSERT WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.github_repository_records ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_repository_records_member ON public.github_repository_records USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.github_webhook_deliveries ENABLE ROW LEVEL SECURITY;

CREATE POLICY github_webhook_deliveries_service ON public.github_webhook_deliveries USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY intelligence_policies_read ON public.space_library_intelligence_policies FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY intelligence_policies_write ON public.space_library_intelligence_policies USING ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id)));

CREATE POLICY legal_holds_service_policy ON public.library_legal_holds USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.library_blobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_blobs_policy ON public.library_blobs USING ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(security_domain_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(security_domain_id)));

ALTER TABLE public.library_derivatives ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_derivatives_policy ON public.library_derivatives USING ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(security_domain_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(security_domain_id)));

ALTER TABLE public.library_exports ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_exports_policy ON public.library_exports USING ((public.misty_rls_is_service() OR ((requested_by_user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((requested_by_user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.library_files ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_files_policy ON public.library_files USING ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(security_domain_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(security_domain_id)));

ALTER TABLE public.library_item_versions ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_item_versions_policy ON public.library_item_versions USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_library_items i
  WHERE ((i.id = library_item_versions.space_library_item_id) AND public.misty_is_space_member(i.space_id)))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_library_items i
  WHERE ((i.id = library_item_versions.space_library_item_id) AND public.misty_is_space_member(i.space_id))))));

ALTER TABLE public.library_legal_holds ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.library_processing_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_processing_jobs_service_policy ON public.library_processing_jobs USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.library_reauthentication_grants ENABLE ROW LEVEL SECURITY;

CREATE POLICY library_reauthentication_grants_policy ON public.library_reauthentication_grants USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.library_recovery_tombstones ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.licenses ENABLE ROW LEVEL SECURITY;

CREATE POLICY licenses_delete_policy ON public.licenses FOR DELETE USING (public.misty_rls_is_service());

CREATE POLICY licenses_insert_policy ON public.licenses FOR INSERT WITH CHECK ((public.misty_rls_is_service() OR ((public.misty_rls_mode() = 'registration'::text) AND (user_id = public.misty_rls_user_id()) AND (id = public.misty_rls_license_id()))));

CREATE POLICY licenses_select_policy ON public.licenses FOR SELECT USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY licenses_update_policy ON public.licenses FOR UPDATE USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.mail_action_audit ENABLE ROW LEVEL SECURITY;

CREATE POLICY mail_action_audit_owner ON public.mail_action_audit USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.mcp_discovery_snapshots ENABLE ROW LEVEL SECURITY;

CREATE POLICY mcp_discovery_snapshots_owner_read ON public.mcp_discovery_snapshots FOR SELECT USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.mcp_remote_connections c
  WHERE ((c.id = mcp_discovery_snapshots.connection_id) AND (c.owner_user_id = public.misty_rls_user_id()))))));

CREATE POLICY mcp_discovery_snapshots_service_write ON public.mcp_discovery_snapshots USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.mcp_oauth_credentials ENABLE ROW LEVEL SECURITY;

CREATE POLICY mcp_oauth_credentials_owner ON public.mcp_oauth_credentials USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.mcp_oauth_states ENABLE ROW LEVEL SECURITY;

CREATE POLICY mcp_oauth_states_owner ON public.mcp_oauth_states USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.mcp_remote_connections ENABLE ROW LEVEL SECURITY;

CREATE POLICY mcp_remote_connections_owner ON public.mcp_remote_connections USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.mcp_remote_tools ENABLE ROW LEVEL SECURITY;

CREATE POLICY mcp_remote_tools_owner_read ON public.mcp_remote_tools FOR SELECT USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.mcp_remote_connections c
  WHERE ((c.id = mcp_remote_tools.connection_id) AND (c.owner_user_id = public.misty_rls_user_id()))))));

CREATE POLICY mcp_remote_tools_service_write ON public.mcp_remote_tools USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.mcp_tool_execution_audit ENABLE ROW LEVEL SECURITY;

CREATE POLICY mcp_tool_execution_audit_owner_read ON public.mcp_tool_execution_audit FOR SELECT USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

CREATE POLICY mcp_tool_execution_audit_service_write ON public.mcp_tool_execution_audit FOR INSERT WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.media_search_assets ENABLE ROW LEVEL SECURITY;

CREATE POLICY media_search_assets_policy ON public.media_search_assets USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.media_search_chunks ENABLE ROW LEVEL SECURITY;

CREATE POLICY media_search_chunks_policy ON public.media_search_chunks USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.media_search_devices ENABLE ROW LEVEL SECURITY;

CREATE POLICY media_search_devices_policy ON public.media_search_devices USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.media_search_segments ENABLE ROW LEVEL SECURITY;

CREATE POLICY media_search_segments_policy ON public.media_search_segments USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY message_library_references_policy ON public.space_message_library_references USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.misty_agent_execution_leases ENABLE ROW LEVEL SECURITY;

CREATE POLICY misty_agent_execution_leases_owner ON public.misty_agent_execution_leases USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.misty_ask_conversation_events ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.misty_ask_conversations ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.misty_ask_identities ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.misty_ask_identity_versions ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.misty_ask_mcp_tools ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.misty_conversation_focus ENABLE ROW LEVEL SECURITY;

CREATE POLICY misty_conversation_focus_owner_policy ON public.misty_conversation_focus USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.misty_conversation_pending_actions ENABLE ROW LEVEL SECURITY;

CREATE POLICY misty_conversation_pending_actions_owner_policy ON public.misty_conversation_pending_actions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.misty_memories ENABLE ROW LEVEL SECURITY;

CREATE POLICY misty_memories_owner_policy ON public.misty_memories USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.native_task_effects ENABLE ROW LEVEL SECURITY;

CREATE POLICY native_task_effects_service ON public.native_task_effects USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.object_deletion_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY object_deletion_jobs_service ON public.object_deletion_jobs USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY owner_policy ON public.agent_model_turn_claims USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.agent_sdk_capability_bindings USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_backend_connection_versions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_backend_connections USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_capability_contract_versions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_capability_invocations USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_provider_registrations USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_provider_versions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_target_versions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY owner_policy ON public.sdk_targets USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.owner_storage_usage ENABLE ROW LEVEL SECURITY;

CREATE POLICY owner_storage_usage_policy ON public.owner_storage_usage USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.password_recovery_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY password_recovery_jobs_service ON public.password_recovery_jobs USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.password_reset_tokens ENABLE ROW LEVEL SECURITY;

CREATE POLICY password_reset_tokens_all_policy ON public.password_reset_tokens USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY personal_agent_mcp_tools_owner ON public.misty_ask_mcp_tools USING ((public.misty_rls_is_service() OR ((owner_user_id = public.misty_rls_user_id()) AND (EXISTS ( SELECT 1
   FROM public.misty_ask_identities a
  WHERE ((a.id = misty_ask_mcp_tools.agent_id) AND (a.owner_user_id = public.misty_rls_user_id())))) AND (EXISTS ( SELECT 1
   FROM public.mcp_remote_connections c
  WHERE ((c.id = misty_ask_mcp_tools.connection_id) AND (c.owner_user_id = public.misty_rls_user_id())))) AND (EXISTS ( SELECT 1
   FROM public.mcp_remote_tools t
  WHERE ((t.id = misty_ask_mcp_tools.remote_tool_id) AND (t.connection_id = t.connection_id))))))) WITH CHECK ((public.misty_rls_is_service() OR ((owner_user_id = public.misty_rls_user_id()) AND (EXISTS ( SELECT 1
   FROM public.misty_ask_identities a
  WHERE ((a.id = misty_ask_mcp_tools.agent_id) AND (a.owner_user_id = public.misty_rls_user_id())))) AND (EXISTS ( SELECT 1
   FROM public.mcp_remote_connections c
  WHERE ((c.id = misty_ask_mcp_tools.connection_id) AND (c.owner_user_id = public.misty_rls_user_id())))) AND (EXISTS ( SELECT 1
   FROM public.mcp_remote_tools t
  WHERE ((t.id = misty_ask_mcp_tools.remote_tool_id) AND (t.connection_id = t.connection_id)))))));

CREATE POLICY personal_agent_task_run_jobs_service_policy ON public.agent_run_jobs USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY personal_agent_versions_owner_read ON public.misty_ask_identity_versions FOR SELECT USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.misty_ask_identities a
  WHERE ((a.id = misty_ask_identity_versions.agent_id) AND (a.owner_user_id = public.misty_rls_user_id()))))));

CREATE POLICY personal_agent_versions_owner_write ON public.misty_ask_identity_versions USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.misty_ask_identities a
  WHERE ((a.id = misty_ask_identity_versions.agent_id) AND (a.owner_user_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.misty_ask_identities a
  WHERE ((a.id = misty_ask_identity_versions.agent_id) AND (a.owner_user_id = public.misty_rls_user_id()))))));

CREATE POLICY personal_agents_owner_policy ON public.misty_ask_identities USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.personal_space_templates ENABLE ROW LEVEL SECURITY;

CREATE POLICY personal_space_templates_owner ON public.personal_space_templates USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.provider_content_records ENABLE ROW LEVEL SECURITY;

CREATE POLICY provider_content_records_member_policy ON public.provider_content_records USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.provider_event_inbox ENABLE ROW LEVEL SECURITY;

CREATE POLICY provider_event_inbox_owner ON public.provider_event_inbox USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.provider_gateway_state ENABLE ROW LEVEL SECURITY;

CREATE POLICY provider_gateway_state_service_policy ON public.provider_gateway_state USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.provider_oauth_states ENABLE ROW LEVEL SECURITY;

CREATE POLICY provider_oauth_states_owner ON public.provider_oauth_states USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.provider_shared_resources ENABLE ROW LEVEL SECURITY;

CREATE POLICY provider_shared_resources_member_policy ON public.provider_shared_resources USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.provider_subscriptions ENABLE ROW LEVEL SECURITY;

CREATE POLICY provider_subscriptions_owner ON public.provider_subscriptions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.realtime_tickets ENABLE ROW LEVEL SECURITY;

CREATE POLICY realtime_tickets_user_policy ON public.realtime_tickets USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

CREATE POLICY recovery_tombstones_policy ON public.library_recovery_tombstones USING ((public.misty_rls_is_service() OR ((space_id IS NOT NULL) AND public.misty_is_space_owner(space_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((space_id IS NOT NULL) AND public.misty_is_space_owner(space_id))));

ALTER TABLE public.sdk_backend_connection_versions ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_backend_connections ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_capability_contract_versions ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_capability_invocations ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_provider_registrations ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_provider_versions ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_target_versions ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.sdk_targets ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.security_domains ENABLE ROW LEVEL SECURITY;

CREATE POLICY security_domains_policy ON public.security_domains USING ((public.misty_rls_is_service() OR public.misty_can_access_security_domain(id))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.self_host_accounts ENABLE ROW LEVEL SECURITY;

CREATE POLICY self_host_accounts_service ON public.self_host_accounts USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.self_host_bootstrap_tokens ENABLE ROW LEVEL SECURITY;

CREATE POLICY self_host_bootstrap_tokens_service ON public.self_host_bootstrap_tokens USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.self_host_collaboration_documents ENABLE ROW LEVEL SECURITY;

CREATE POLICY self_host_collaboration_documents_service ON public.self_host_collaboration_documents USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.self_host_enrollment_invitations ENABLE ROW LEVEL SECURITY;

CREATE POLICY self_host_enrollment_invitations_service ON public.self_host_enrollment_invitations USING ((public.misty_rls_is_service() OR (created_by = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (created_by = public.misty_rls_user_id())));

ALTER TABLE public.sessions ENABLE ROW LEVEL SECURITY;

CREATE POLICY sessions_delete_policy ON public.sessions FOR DELETE USING ((public.misty_rls_is_service() OR ((public.misty_rls_mode() = 'session'::text) AND (token_hash = public.misty_rls_session_token_hash()))));

CREATE POLICY sessions_insert_policy ON public.sessions FOR INSERT WITH CHECK ((public.misty_rls_is_service() OR ((public.misty_rls_mode() = 'session'::text) AND (token_hash = public.misty_rls_session_token_hash()) AND (user_id = public.misty_rls_user_id()))));

CREATE POLICY sessions_select_policy ON public.sessions FOR SELECT USING ((public.misty_rls_is_service() OR ((public.misty_rls_mode() = 'session'::text) AND (token_hash = public.misty_rls_session_token_hash()))));

CREATE POLICY sessions_update_policy ON public.sessions FOR UPDATE USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.smart_library_assets ENABLE ROW LEVEL SECURITY;

CREATE POLICY smart_library_assets_policy ON public.smart_library_assets USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.smart_library_batches ENABLE ROW LEVEL SECURITY;

CREATE POLICY smart_library_batches_policy ON public.smart_library_batches USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.smart_library_folders f
  WHERE ((f.id = smart_library_batches.folder_id) AND (f.user_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.smart_library_folders f
  WHERE ((f.id = smart_library_batches.folder_id) AND (f.user_id = public.misty_rls_user_id()))))));

ALTER TABLE public.smart_library_cost_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY smart_library_cost_events_policy ON public.smart_library_cost_events USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.smart_library_folders ENABLE ROW LEVEL SECURITY;

CREATE POLICY smart_library_folders_policy ON public.smart_library_folders USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.smart_library_reindex_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY smart_library_reindex_jobs_policy ON public.smart_library_reindex_jobs USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.social_automation_rules ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_automation_rules_member ON public.social_automation_rules USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.social_automation_runs ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_automation_runs_member ON public.social_automation_runs FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.social_bindings ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_bindings_member ON public.social_bindings USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.social_identities ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_identities_member ON public.social_identities USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.social_bindings b
  WHERE ((b.id = social_identities.binding_id) AND public.misty_is_space_member(b.space_id)))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.social_bindings b
  WHERE ((b.id = social_identities.binding_id) AND public.misty_is_space_member(b.space_id))))));

ALTER TABLE public.social_outbound_commands ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_outbound_commands_member ON public.social_outbound_commands USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.social_scheduled_messages ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_scheduled_messages_member ON public.social_scheduled_messages USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.social_send_authorities ENABLE ROW LEVEL SECURITY;

CREATE POLICY social_send_authorities_owner ON public.social_send_authorities USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_album_folders ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_album_folders_policy ON public.space_album_folders USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_album_items ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_album_items_policy ON public.space_album_items USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_albums a
  WHERE ((a.id = space_album_items.album_id) AND public.misty_is_space_member(a.space_id)))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_albums a
  WHERE ((a.id = space_album_items.album_id) AND public.misty_is_space_member(a.space_id))))));

ALTER TABLE public.space_albums ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_albums_policy ON public.space_albums USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_calendar_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_calendar_events_member_policy ON public.space_calendar_events USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_calendar_sources ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_calendar_sources_member_policy ON public.space_calendar_sources USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_conversation_members ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_conversation_members_creator_write ON public.space_conversation_members USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_conversations c
  WHERE ((c.id = space_conversation_members.conversation_id) AND (c.created_by_user_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_conversations c
  WHERE ((c.id = space_conversation_members.conversation_id) AND (c.created_by_user_id = public.misty_rls_user_id()))))));

CREATE POLICY space_conversation_members_read ON public.space_conversation_members FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_conversation_member(conversation_id)));

ALTER TABLE public.space_conversation_reads ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_conversation_reads_policy ON public.space_conversation_reads USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_conversations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_conversations_create ON public.space_conversations FOR INSERT WITH CHECK ((public.misty_rls_is_service() OR ((created_by_user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

CREATE POLICY space_conversations_creator_delete ON public.space_conversations FOR DELETE USING ((public.misty_rls_is_service() OR (created_by_user_id = public.misty_rls_user_id())));

CREATE POLICY space_conversations_creator_write ON public.space_conversations FOR UPDATE USING ((public.misty_rls_is_service() OR (created_by_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (created_by_user_id = public.misty_rls_user_id())));

CREATE POLICY space_conversations_disconnected_discord_owner_delete ON public.space_conversations FOR DELETE USING ((public.misty_rls_is_service() OR ((origin = 'discord'::text) AND (integration_status = 'disconnected'::text) AND public.misty_is_space_owner(space_id))));

CREATE POLICY space_conversations_read ON public.space_conversations FOR SELECT USING ((public.misty_rls_is_service() OR (created_by_user_id = public.misty_rls_user_id()) OR public.misty_is_space_conversation_member(id)));

ALTER TABLE public.space_creation_requests ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_creation_requests_owner ON public.space_creation_requests USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_device_presence ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_device_presence_owner_read ON public.space_device_presence FOR SELECT USING ((owner_user_id = public.misty_rls_user_id()));

CREATE POLICY space_device_presence_service ON public.space_device_presence USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.space_discord_links ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_discord_links_member_policy ON public.space_discord_links USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_drawing_assets ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_drawing_assets_access_policy ON public.space_drawing_assets USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM (public.space_drawings d
     JOIN public.space_members m ON ((m.space_id = d.space_id)))
  WHERE ((d.id = space_drawing_assets.drawing_id) AND (d.lifecycle_state = 'active'::text) AND (m.user_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM (public.space_drawings d
     JOIN public.space_members m ON ((m.space_id = d.space_id)))
  WHERE ((d.id = space_drawing_assets.drawing_id) AND (d.lifecycle_state = 'active'::text) AND (m.user_id = public.misty_rls_user_id()))))));

ALTER TABLE public.space_drawing_control_outbox ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_drawing_control_outbox_policy ON public.space_drawing_control_outbox USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.space_drawings ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_drawings_audience_policy ON public.space_drawings USING ((public.misty_rls_is_service() OR ((lifecycle_state = 'active'::text) AND public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((creator_user_id = public.misty_rls_user_id()) AND public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))));

ALTER TABLE public.space_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_events_read ON public.space_events FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY space_events_write ON public.space_events USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_inbox_items ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_inbox_user_policy ON public.space_inbox_items USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_integrations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_integrations_policy ON public.space_integrations USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_invitation_delivery_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_invitation_delivery_jobs_service ON public.space_invitation_delivery_jobs USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.space_invitations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_invites_owner_write ON public.space_invitations USING ((public.misty_rls_is_service() OR (invited_user_id = public.misty_rls_user_id()) OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id)));

CREATE POLICY space_invites_read ON public.space_invitations FOR SELECT USING ((public.misty_rls_is_service() OR (invited_user_id = public.misty_rls_user_id()) OR public.misty_is_space_owner(space_id)));

ALTER TABLE public.space_item_aliases ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_item_aliases_policy ON public.space_item_aliases USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_library_asset_stack_members ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_asset_stack_members_policy ON public.space_library_asset_stack_members USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_library_asset_stacks s
  WHERE ((s.id = space_library_asset_stack_members.stack_id) AND public.misty_is_space_member(s.space_id)))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_library_asset_stacks s
  WHERE ((s.id = space_library_asset_stack_members.stack_id) AND public.misty_is_space_member(s.space_id))))));

ALTER TABLE public.space_library_asset_stacks ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_asset_stacks_policy ON public.space_library_asset_stacks USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_library_audit_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_audit_insert ON public.space_library_audit_events FOR INSERT WITH CHECK ((public.misty_rls_is_service() OR ((space_id IS NOT NULL) AND public.misty_is_space_member(space_id))));

CREATE POLICY space_library_audit_policy ON public.space_library_audit_events FOR SELECT USING ((public.misty_rls_is_service() OR ((space_id IS NOT NULL) AND public.misty_is_space_owner(space_id))));

ALTER TABLE public.space_library_direct_references ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.space_library_grants ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_grants_policy ON public.space_library_grants USING ((public.misty_rls_is_service() OR public.misty_is_space_member(source_space_id) OR public.misty_is_space_member(destination_space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(source_space_id)));

ALTER TABLE public.space_library_groups ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_groups_policy ON public.space_library_groups USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_library_imports ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_imports_insert ON public.space_library_imports FOR INSERT WITH CHECK ((public.misty_rls_is_service() OR ((importer_user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(destination_space_id))));

CREATE POLICY space_library_imports_policy ON public.space_library_imports FOR SELECT USING ((public.misty_rls_is_service() OR ((importer_user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(destination_space_id))));

ALTER TABLE public.space_library_intelligence_policies ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.space_library_item_views ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_item_views_policy ON public.space_library_item_views USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.space_library_items ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_items_audience_policy ON public.space_library_items USING ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

ALTER TABLE public.space_library_search_documents ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_search_documents_policy ON public.space_library_search_documents FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY space_library_search_documents_service_write ON public.space_library_search_documents USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.space_library_uploads ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_library_uploads_policy ON public.space_library_uploads USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

CREATE POLICY space_member_overrides_owner_write ON public.space_member_permission_overrides USING ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id)));

CREATE POLICY space_member_overrides_read ON public.space_member_permission_overrides FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_member_permission_overrides ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.space_member_roles ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_member_roles_owner_write ON public.space_member_roles USING ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id)));

CREATE POLICY space_member_roles_read ON public.space_member_roles FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_member_storage_usage ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_member_storage_usage_policy ON public.space_member_storage_usage USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)) OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.space_members ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_members_owner_write ON public.space_members USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY space_members_read ON public.space_members FOR SELECT USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_memory_preferences ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_memory_preferences_policy ON public.space_memory_preferences USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_message_attachments ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_message_attachments_policy ON public.space_message_attachments USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_message_library_references ENABLE ROW LEVEL SECURITY;

ALTER TABLE public.space_message_reactions ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_message_reactions_member_policy ON public.space_message_reactions USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_messages ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_messages_conversation_policy ON public.space_messages USING ((public.misty_rls_is_service() OR ((conversation_id IS NULL) AND public.misty_is_space_member(space_id)) OR ((conversation_id IS NOT NULL) AND public.misty_is_space_conversation_member(conversation_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((conversation_id IS NULL) AND public.misty_is_space_member(space_id)) OR ((conversation_id IS NOT NULL) AND public.misty_is_space_conversation_member(conversation_id))));

ALTER TABLE public.space_native_calendar_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_native_calendar_events_audience_policy ON public.space_native_calendar_events USING ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

ALTER TABLE public.space_nodes ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_nodes_member_policy ON public.space_nodes USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_note_assets ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_note_assets_policy ON public.space_note_assets USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_notes n
  WHERE (n.id = space_note_assets.note_id))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_notes n
  WHERE (n.id = space_note_assets.note_id)))));

ALTER TABLE public.space_note_control_outbox ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_note_control_outbox_policy ON public.space_note_control_outbox USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

ALTER TABLE public.space_note_links ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_note_links_policy ON public.space_note_links USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_notes n
  WHERE (n.id = space_note_links.source_note_id))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_notes n
  WHERE (n.id = space_note_links.source_note_id)))));

ALTER TABLE public.space_note_permissions ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_note_permissions_policy ON public.space_note_permissions USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()) OR (EXISTS ( SELECT 1
   FROM public.space_notes n
  WHERE ((n.id = space_note_permissions.note_id) AND (n.creator_user_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_notes n
  WHERE ((n.id = space_note_permissions.note_id) AND (n.creator_user_id = public.misty_rls_user_id()))))));

ALTER TABLE public.space_notes ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_notes_audience_policy ON public.space_notes USING ((public.misty_rls_is_service() OR ((lifecycle_state = 'active'::text) AND public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((creator_user_id = public.misty_rls_user_id()) AND public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))));

ALTER TABLE public.space_people ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_people_policy ON public.space_people USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_person_observations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_person_observations_policy ON public.space_person_observations USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_people p
  WHERE ((p.id = space_person_observations.person_id) AND public.misty_is_space_member(p.space_id)))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_people p
  WHERE ((p.id = space_person_observations.person_id) AND public.misty_is_space_member(p.space_id))))));

ALTER TABLE public.space_pinned_collections ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_pinned_collections_policy ON public.space_pinned_collections USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_provider_credentials ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_provider_credentials_owner ON public.space_provider_credentials USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()) OR (EXISTS ( SELECT 1
   FROM public.spaces s
  WHERE ((s.id = space_provider_credentials.space_id) AND (s.owner_user_id = public.misty_rls_user_id())))) OR (EXISTS ( SELECT 1
   FROM public.provider_shared_resources r
  WHERE ((r.integration_id = space_provider_credentials.integration_id) AND (r.space_id = space_provider_credentials.space_id) AND (r.status = 'active'::text) AND public.misty_is_space_member(r.space_id)))))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()) OR (EXISTS ( SELECT 1
   FROM public.spaces s
  WHERE ((s.id = space_provider_credentials.space_id) AND (s.owner_user_id = public.misty_rls_user_id())))) OR (EXISTS ( SELECT 1
   FROM public.provider_shared_resources r
  WHERE ((r.integration_id = space_provider_credentials.integration_id) AND (r.space_id = space_provider_credentials.space_id) AND (r.status = 'active'::text) AND public.misty_is_space_member(r.space_id))))));

ALTER TABLE public.space_rendition_reservations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_rendition_reservations_policy ON public.space_rendition_reservations USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)) OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.space_resolve_tickets ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_resolve_tickets_user_policy ON public.space_resolve_tickets USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_roadmap_edges ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmap_edges_member_policy ON public.space_roadmap_edges USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_roadmap_goal_tasks ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmap_goal_tasks_member_policy ON public.space_roadmap_goal_tasks USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_roadmap_goals ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmap_goals_member_policy ON public.space_roadmap_goals USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_roadmap_milestones ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmap_milestones_member_policy ON public.space_roadmap_milestones USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_roadmap_node_definitions ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmap_node_definitions_member_policy ON public.space_roadmap_node_definitions USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_roadmap_nodes ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmap_nodes_member_policy ON public.space_roadmap_nodes USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_roadmaps ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roadmaps_audience_policy ON public.space_roadmaps USING ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

ALTER TABLE public.space_roles ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_roles_owner_write ON public.space_roles USING ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id)));

CREATE POLICY space_roles_read ON public.space_roles FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_run_actions ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_run_actions_policy ON public.space_run_actions USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE (r.id = space_run_actions.run_id))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE (r.id = space_run_actions.run_id)))));

ALTER TABLE public.space_run_approvals ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_run_approvals_policy ON public.space_run_approvals USING ((public.misty_rls_is_service() OR (requested_from_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (requested_from_user_id = public.misty_rls_user_id())));

ALTER TABLE public.space_run_steps ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_run_steps_private ON public.space_run_steps USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE ((r.id = space_run_steps.run_id) AND (r.requesting_member_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE ((r.id = space_run_steps.run_id) AND (r.requesting_member_id = public.misty_rls_user_id()))))));

ALTER TABLE public.space_runs ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_runs_private_or_shared_policy ON public.space_runs USING ((public.misty_rls_is_service() OR ((agent_id IS NOT NULL) AND (owner_user_id = public.misty_rls_user_id())) OR ((agent_id IS NULL) AND ((requesting_member_id = public.misty_rls_user_id()) OR public.misty_is_shared_space_run_visible(space_id, source_type, source_conversation_id))))) WITH CHECK ((public.misty_rls_is_service() OR ((agent_id IS NOT NULL) AND (owner_user_id = public.misty_rls_user_id())) OR ((agent_id IS NULL) AND ((requesting_member_id = public.misty_rls_user_id()) OR public.misty_is_shared_space_run_visible(space_id, source_type, source_conversation_id)))));

ALTER TABLE public.space_setup_integrations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_setup_integrations_owner_write ON public.space_setup_integrations USING ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_owner(space_id)));

CREATE POLICY space_setup_integrations_read ON public.space_setup_integrations FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_slack_links ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_slack_links_member ON public.space_slack_links USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_storage_contributions ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_storage_contributions_policy ON public.space_storage_contributions USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)) OR public.misty_is_space_owner(space_id))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.space_storage_usage ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_storage_usage_policy ON public.space_storage_usage USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_task_activity ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_task_activity_member_read ON public.space_task_activity FOR SELECT USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

CREATE POLICY space_task_activity_member_write ON public.space_task_activity USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_tasks ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_tasks_audience_policy ON public.space_tasks USING ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

ALTER TABLE public.space_upload_reservations ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_upload_reservations_policy ON public.space_upload_reservations USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)))) WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

ALTER TABLE public.space_workflow_action_journal ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_workflow_action_journal_private ON public.space_workflow_action_journal USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE ((r.id = space_workflow_action_journal.run_id) AND (r.requesting_member_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE ((r.id = space_workflow_action_journal.run_id) AND (r.requesting_member_id = public.misty_rls_user_id()))))));

ALTER TABLE public.space_workflow_resource_leases ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_workflow_resource_leases_private ON public.space_workflow_resource_leases USING ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE ((r.id = space_workflow_resource_leases.run_id) AND (r.requesting_member_id = public.misty_rls_user_id())))))) WITH CHECK ((public.misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM public.space_runs r
  WHERE ((r.id = space_workflow_resource_leases.run_id) AND (r.requesting_member_id = public.misty_rls_user_id()))))));

ALTER TABLE public.space_workflow_versions ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_workflow_versions_policy ON public.space_workflow_versions USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.space_workflows ENABLE ROW LEVEL SECURITY;

CREATE POLICY space_workflows_member_policy ON public.space_workflows USING ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id))) WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(space_id)));

ALTER TABLE public.spaces ENABLE ROW LEVEL SECURITY;

CREATE POLICY spaces_owner_write ON public.spaces USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE POLICY spaces_read ON public.spaces FOR SELECT USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()) OR (EXISTS ( SELECT 1
   FROM public.space_members member
  WHERE ((member.space_id = spaces.id) AND (member.user_id = public.misty_rls_user_id()))))));

ALTER TABLE public.trusted_device_request_nonces ENABLE ROW LEVEL SECURITY;

CREATE POLICY trusted_device_request_nonces_user_policy ON public.trusted_device_request_nonces USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())));

ALTER TABLE public.trusted_devices ENABLE ROW LEVEL SECURITY;

CREATE POLICY trusted_devices_user_policy ON public.trusted_devices USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.user_app_activity ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_app_activity_owner_policy ON public.user_app_activity USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.user_global_home_activity ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_global_home_activity_owner_policy ON public.user_global_home_activity USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.user_home_activity ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_home_activity_owner_policy ON public.user_home_activity USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));

ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;

CREATE POLICY users_delete_policy ON public.users FOR DELETE USING (public.misty_rls_is_service());

CREATE POLICY users_insert_policy ON public.users FOR INSERT WITH CHECK (((public.misty_rls_mode() = 'registration'::text) AND (id = public.misty_rls_user_id()) AND (license_id = public.misty_rls_license_id()) AND (lower(email) = public.misty_rls_email())));

CREATE POLICY users_select_policy ON public.users FOR SELECT USING ((public.misty_rls_is_service() OR (id = public.misty_rls_user_id()) OR ((public.misty_rls_mode() = 'anonymous'::text) AND (lower(email) = public.misty_rls_email()))));

CREATE POLICY users_update_policy ON public.users FOR UPDATE USING ((public.misty_rls_is_service() OR (id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (id = public.misty_rls_user_id())));

ALTER TABLE public.workflow_device_node_jobs ENABLE ROW LEVEL SECURITY;

CREATE POLICY workflow_device_node_jobs_owner ON public.workflow_device_node_jobs USING ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id()))) WITH CHECK ((public.misty_rls_is_service() OR (user_id = public.misty_rls_user_id())));
$misty_schema$;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  EXECUTE $misty_grants$
GRANT ALL ON FUNCTION public.misty_can_access_space_audience(candidate_space_id text, candidate_audience_kind text, candidate_conversation_id text) TO misty_app;
GRANT ALL ON FUNCTION public.misty_note_permission_guard() TO misty_app;
REVOKE ALL ON FUNCTION public.queue_removed_ai_attachment_objects() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.queue_replaced_user_avatar() FROM PUBLIC;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.abuse_blocks TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.account_deletion_provider_resources TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.account_deletion_requests TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.account_deletion_steps TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_ask_conversation_events TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.agent_conversation_events_id_seq TO misty_app;
GRANT SELECT,INSERT ON TABLE public.agent_model_turn_claims TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.agent_run_contexts TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.agent_run_jobs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.agent_run_tool_approvals TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.agent_runtime_deliveries TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.agent_runtime_start_receipts TO misty_app;
GRANT SELECT,INSERT ON TABLE public.agent_sdk_capability_bindings TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.agent_toolbox_action_journal TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_artifacts TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_cleanup_jobs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_conversation_attachments TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_feature_flags TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_feedback TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_intervention_waits TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_invocation_contexts TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_invocation_events TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_invocations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_recaps TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_retrieval_chunks TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_retrieval_documents TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_runtime_callback_receipts TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_surface_preferences TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.ai_user_settings TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.cloud_connections TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.cloud_credential_handoffs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.cloud_oauth_states TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.connected_account_oauth_states TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.connected_accounts TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.connection_authorization_requests TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.device_pairing_sessions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.device_pairs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.device_presence TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.figma_comment_audit TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.figma_content_records TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.figma_space_bindings TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.figma_webhook_deliveries TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.figma_webhook_subscriptions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_app_installations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_app_setup_states TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_code_workspaces TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_credential_handoffs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_mutation_audit TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_repository_records TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.github_webhook_deliveries TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_blobs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_derivatives TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_exports TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_files TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_item_versions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_legal_holds TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_processing_jobs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_reauthentication_grants TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.library_recovery_tombstones TO misty_app;
GRANT SELECT,INSERT ON TABLE public.mail_action_audit TO misty_app;
GRANT UPDATE(target_id) ON TABLE public.mail_action_audit TO misty_app;
GRANT UPDATE(success) ON TABLE public.mail_action_audit TO misty_app;
GRANT UPDATE(error_code) ON TABLE public.mail_action_audit TO misty_app;
GRANT UPDATE(completed_at) ON TABLE public.mail_action_audit TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.mail_action_audit_id_seq TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.mcp_discovery_snapshots TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.mcp_oauth_credentials TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.mcp_oauth_states TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.mcp_remote_connections TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.mcp_remote_tools TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.mcp_tool_execution_audit TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.media_search_assets TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.media_search_chunks TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.media_search_devices TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.media_search_segments TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_agent_execution_leases TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_ask_conversations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_ask_identities TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_ask_identity_versions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_ask_mcp_tools TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_conversation_focus TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_conversation_pending_actions TO misty_app;
GRANT SELECT,INSERT,UPDATE ON TABLE public.misty_instance TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.misty_memories TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.native_task_effects TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.object_deletion_jobs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.owner_storage_usage TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.password_recovery_jobs_request_order_seq TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.personal_space_templates TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.provider_content_records TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.provider_event_inbox TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.provider_event_inbox_id_seq TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.provider_gateway_state TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.provider_oauth_states TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.provider_shared_resources TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.provider_subscriptions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_backend_connection_versions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_backend_connections TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_capability_contract_versions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_capability_invocations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_provider_registrations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_provider_versions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_target_versions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.sdk_targets TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.security_domains TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.self_host_accounts TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.self_host_bootstrap_tokens TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.self_host_collaboration_documents TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.self_host_enrollment_invitations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.smart_library_reindex_jobs TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.smart_library_result_sequence TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_automation_rules TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_automation_runs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_bindings TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_identities TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_outbound_commands TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_scheduled_messages TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.social_send_authorities TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_album_folders TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_album_items TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_albums TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_calendar_events TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_calendar_sources TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_conversation_members TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_conversation_reads TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_conversations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_creation_requests TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_device_presence TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_discord_links TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_drawing_assets TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_drawing_control_outbox TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_drawings TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.space_events_id_seq TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.space_inbox_items_id_seq TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_integrations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_invitation_delivery_jobs TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_item_aliases TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_asset_stack_members TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_asset_stacks TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_audit_events TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.space_library_audit_events_id_seq TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_direct_references TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_grants TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_groups TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_imports TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_intelligence_policies TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_item_views TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_items TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_search_documents TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_library_uploads TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_member_permission_overrides TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_member_roles TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_member_storage_usage TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_memory_preferences TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_message_attachments TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_message_library_references TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_message_reactions TO misty_app;
GRANT SELECT,USAGE ON SEQUENCE public.space_messages_seq_seq TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_native_calendar_events TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_note_assets TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_note_control_outbox TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_note_links TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_note_permissions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_notes TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_people TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_person_observations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_pinned_collections TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_provider_credentials TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_rendition_reservations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmap_edges TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmap_goal_tasks TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmap_goals TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmap_milestones TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmap_node_definitions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmap_nodes TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roadmaps TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_roles TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_run_actions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_run_approvals TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_run_steps TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_setup_integrations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_slack_links TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_storage_contributions TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_storage_usage TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_task_activity TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_tasks TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_upload_reservations TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_workflow_action_journal TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_workflow_resource_leases TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.space_workflow_versions TO misty_app;
GRANT SELECT,INSERT,DELETE ON TABLE public.trusted_device_request_nonces TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.trusted_devices TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.user_app_activity TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.user_global_home_activity TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.user_home_activity TO misty_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.workflow_device_node_jobs TO misty_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,USAGE ON SEQUENCES TO misty_app;
$misty_grants$;
 END IF;
 INSERT INTO public.ai_feature_flags(surface_id,action_id,model_id,enabled,rollout_percent) VALUES('*','*','*',TRUE,100);
 ELSE
  SELECT COALESCE(MAX(version_id),0) INTO previous_version FROM public.goose_db_version WHERE is_applied;
  IF previous_version < 20270215120000 THEN
   RAISE EXCEPTION 'Upgrade this historical database to 20270215120000 with the archived operator migrations before applying the browser baseline';
  END IF;
 IF previous_version < 20270216000000 THEN
  EXECUTE $misty_upgrade$

-- Provider-neutral delivery records. Existing billing and retired app data is
-- intentionally untouched until the separately rehearsed archival/cutover.
CREATE TABLE billing_adapter_outbox (
 id text PRIMARY KEY,
 action text NOT NULL CHECK(action IN ('settle','release','refund')),
 payload jsonb NOT NULL,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 delivered_at timestamptz
);
CREATE INDEX billing_adapter_outbox_due ON billing_adapter_outbox(available_at,created_at) WHERE delivered_at IS NULL;
CREATE TABLE billing_adapter_reservations (
 account_id text NOT NULL,
 key text NOT NULL,
 reservation_id text NOT NULL,
 admission jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,key)
);
$misty_upgrade$;
 END IF;
 IF previous_version < 20270217000000 THEN
  EXECUTE $misty_upgrade$

ALTER TABLE billing_adapter_outbox DROP CONSTRAINT billing_adapter_outbox_action_check;
ALTER TABLE billing_adapter_outbox ADD CONSTRAINT billing_adapter_outbox_action_check
 CHECK(action IN ('settle','release','refund','settle_group','release_group'));
CREATE INDEX billing_adapter_reservations_operation ON billing_adapter_reservations(account_id,(admission->>'operation_id'));
$misty_upgrade$;
 END IF;
 IF previous_version < 20270218000000 THEN
  EXECUTE $misty_upgrade$

-- An admission must survive a crash between the remote reservation and the
-- local receipt. Expired attempts can be replayed and released without running
-- their provider work. No charging policy is stored here.
CREATE TABLE billing_adapter_intents (
 account_id text NOT NULL,
 key text NOT NULL,
 admission jsonb NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','admitted','abandoned','recovered')),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '2 minutes',
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,key)
);
CREATE INDEX billing_adapter_intents_expired ON billing_adapter_intents(expires_at) WHERE state IN ('pending','abandoned');
$misty_upgrade$;
 END IF;
 IF previous_version < 20270219000000 THEN
  EXECUTE $misty_upgrade$

SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
-- The old data remains intact, including keys, constraints and timestamps.
-- Runtime roles receive no usage privilege on this operator-only archive.
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['misty_routines','misty_routine_versions','misty_routine_runs','misty_routine_agent_steps','misty_routine_agent_calls','misty_routine_waits','waitlist_signups'] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
END $$;
$misty_upgrade$;
 END IF;
 IF previous_version < 20270220000000 THEN
  EXECUTE $misty_upgrade$

-- Durable provider-attempt ownership prevents concurrent retrieval workers from
-- issuing duplicate embeddings. Raw usage settles through the billing outbox.
ALTER TABLE ai_retrieval_chunks ADD COLUMN embedding_attempt_id uuid;
ALTER TABLE ai_retrieval_chunks ADD COLUMN embedding_lease_until timestamptz;
CREATE INDEX ai_retrieval_embedding_due ON ai_retrieval_chunks(embedding_lease_until,updated_at) WHERE embedding IS NULL;
$misty_upgrade$;
 END IF;
 IF previous_version < 20270221000000 THEN
  EXECUTE $misty_upgrade$

ALTER TABLE billing_adapter_outbox DROP CONSTRAINT billing_adapter_outbox_action_check;
ALTER TABLE billing_adapter_outbox ADD CONSTRAINT billing_adapter_outbox_action_check
 CHECK(action IN ('settle','release','refund','settle_group','release_group','close'));
$misty_upgrade$;
 END IF;
 IF previous_version < 20270222000000 THEN
  EXECUTE $misty_upgrade$

-- Existing customer data is preserved verbatim. The application uses only the
-- adapter; the operator exports/reconciles this archive before enabling billing.
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
DO $$ DECLARE relation text; held bigint; BEGIN
 IF to_regclass('public.hosted_ai_reservations') IS NOT NULL THEN
  SELECT count(*) INTO held FROM public.hosted_ai_reservations WHERE status='reserved';
  IF held>0 THEN RAISE EXCEPTION 'Drain legacy billing reservations before archival'; END IF;
 END IF;
 FOREACH relation IN ARRAY ARRAY[
  'stripe_subscriptions','stripe_purchases','stripe_subscription_checkout_attempts','stripe_webhook_events',
  'hosted_ai_wallets','hosted_ai_reservations','hosted_ai_usage_ledger','space_hosted_ai_wallets',
  'credit_wallets','credit_reservations','credit_ledger','credit_purchases',
  'license_lifetime_grants','payment_entitlement_inbox','payment_entitlement_projections'
 ] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
END $$;
$misty_upgrade$;
 END IF;
 IF previous_version < 20270223000000 THEN
  EXECUTE $misty_upgrade$

SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
-- File sharing is owned by the account and its trusted devices. Pairing,
-- revocation and fresh endpoint checks remain mandatory at ticket issuance.
ALTER TABLE space_device_presence DROP CONSTRAINT IF EXISTS space_device_presence_owner_user_id_app_id_fkey;
DROP POLICY IF EXISTS space_device_presence_owner_read ON space_device_presence;
CREATE POLICY space_device_presence_owner_read ON space_device_presence FOR SELECT
 USING(owner_user_id=misty_rls_user_id());
-- Old presence cannot issue tickets with the first-party protocol generation.
-- Preserve records until each device publishes fresh presence.
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['payment_purchase_reversals','onboarding_completions'] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
END $$;
$misty_upgrade$;
 END IF;
 IF previous_version < 20270224000000 THEN
  EXECUTE $misty_upgrade$

SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
-- Keep connected-provider definitions and immutable execution revisions under
-- their account. Retired install records no longer grant or revoke this access.
ALTER TABLE public.sdk_provider_registrations DROP CONSTRAINT IF EXISTS sdk_provider_registrations_user_id_app_id_fkey;
ALTER TABLE public.sdk_provider_registrations ADD CONSTRAINT sdk_provider_registrations_account_fkey FOREIGN KEY(user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.sdk_backend_connections DROP CONSTRAINT IF EXISTS sdk_backend_connections_user_id_app_id_fkey;
ALTER TABLE public.sdk_backend_connections ADD CONSTRAINT sdk_backend_connections_account_fkey FOREIGN KEY(user_id) REFERENCES public.users(id) ON DELETE CASCADE;
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['app_data_deletion_jobs','app_install_events','app_personal_records','app_runtime_sessions','misty_agent_app_assignments','sdk_app_manifest_versions','sdk_app_publishers','space_app_installations','user_app_installations'] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
 IF to_regprocedure('public.advance_app_authority_generation()') IS NOT NULL THEN
  ALTER FUNCTION public.advance_app_authority_generation() SET SCHEMA misty_archive;
 END IF;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  REVOKE ALL ON SCHEMA misty_archive FROM misty_app;
  REVOKE ALL ON ALL TABLES IN SCHEMA misty_archive FROM misty_app;
  REVOKE ALL ON ALL SEQUENCES IN SCHEMA misty_archive FROM misty_app;
 END IF;
END $$;
$misty_upgrade$;
 END IF;
 END IF;
END $misty_migration$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Browser baseline is forward-only; use the verified backup for operator recovery'; END $$;
-- +goose StatementEnd
