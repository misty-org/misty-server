-- +goose Up
-- Spaces keep only their primitives: chat, notes, drawings, files and code.
-- Roadmaps, the agenda and Misty's own calendar, tasks, templates, roles and
-- per-member permission overrides, and the Library's extras (albums, people,
-- stacks, groups, pins, imports, cross-Space grants, discovery and the Smart
-- Library's background AI) are removed with their data.

-- A personal Space no longer gets an @everyone role: members share one fixed
-- rule (owners manage the Space; everyone else can do everything else).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_ensure_personal_space(target_user_id text)
 RETURNS text
 LANGUAGE plpgsql
 SECURITY DEFINER
 SET search_path TO 'pg_catalog', 'public', 'pg_temp'
 SET row_security TO 'off'
AS $function$
DECLARE
  existing text;
  first_name text;
  new_space_id text := 'space_' || gen_random_uuid()::text;
  new_domain_id text := 'sd_' || gen_random_uuid()::text;
BEGIN
  -- Same lock the app takes when it creates a Space for this owner.
  PERFORM pg_advisory_xact_lock(hashtext('spaces:owner:' || target_user_id));
  SELECT id INTO existing FROM spaces
    WHERE owner_user_id=target_user_id AND is_default AND lifecycle_state='active';
  IF existing IS NOT NULL THEN
    RETURN existing;
  END IF;
  SELECT left(NULLIF(split_part(btrim(name), ' ', 1), ''), 64) INTO first_name
    FROM users WHERE id=target_user_id AND lifecycle_state='active';
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES(new_domain_id,'space',target_user_id,new_space_id);
  INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
    VALUES(new_space_id,target_user_id,COALESCE(first_name || '''s Space','Personal Space'),new_domain_id,TRUE);
  INSERT INTO space_storage_usage(space_id) VALUES(new_space_id);
  INSERT INTO space_members(space_id,user_id,role) VALUES(new_space_id,target_user_id,'owner');
  RETURN new_space_id;
END $function$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS clear_roadmap_manual_completion_for_reopened_task() CASCADE;

ALTER TABLE space_runs DROP COLUMN IF EXISTS source_task_id;
ALTER TABLE agent_run_jobs DROP COLUMN IF EXISTS task_id;

DROP TABLE IF EXISTS
  space_roadmap_goal_tasks, space_roadmap_edges, space_roadmap_nodes, space_roadmap_node_definitions,
  space_roadmap_goals, space_roadmap_milestones, space_roadmaps,
  space_native_calendar_events, space_calendar_events, space_calendar_sources,
  space_task_activity, space_task_counters, space_tasks,
  personal_space_templates, space_setup_integrations,
  space_member_permission_overrides, space_roles,
  space_album_items, space_album_folders, space_albums,
  space_person_observations, space_people,
  space_library_asset_stack_members, space_library_asset_stacks,
  space_library_groups, space_pinned_collections, space_library_imports,
  space_library_intelligence_policies, space_library_grants, space_memory_preferences,
  space_library_search_documents,
  smart_library_reindex_jobs, smart_library_cost_events, smart_library_batches,
  smart_library_assets, smart_library_folders,
  media_search_segments, media_search_chunks, media_search_assets, media_search_devices
  CASCADE;

-- The AI and face-grouping job kinds no longer have workers.
DELETE FROM library_processing_jobs WHERE job_kind IN ('ai','faces');

-- +goose Down
-- Removed features are not restored; their data was removed in Up.
SELECT 1;
