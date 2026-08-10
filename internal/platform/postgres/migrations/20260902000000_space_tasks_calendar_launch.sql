-- +goose Up
-- +goose StatementBegin
-- Shared Space coordination data. Provider credentials remain private; only
-- explicitly published resource metadata and normalized records are shared.
CREATE TABLE space_tasks (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 240),
    notes TEXT NOT NULL DEFAULT '' CHECK (char_length(notes) <= 20000),
    status TEXT NOT NULL DEFAULT 'todo' CHECK (status IN ('todo','in_progress','done','canceled')),
    assignee_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    due_at TIMESTAMPTZ,
    due_timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (char_length(due_timezone) BETWEEN 1 AND 80),
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(source_refs)='array'),
    created_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_by_agent_id TEXT REFERENCES space_agents(id) ON DELETE SET NULL,
    source_run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    version BIGINT NOT NULL DEFAULT 1,
    completed_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (created_by_user_id IS NOT NULL OR created_by_agent_id IS NOT NULL)
);
CREATE INDEX space_tasks_list_idx ON space_tasks(space_id,archived_at,status,due_at,id);
CREATE INDEX space_tasks_assignee_idx ON space_tasks(space_id,assignee_user_id,archived_at,due_at);

CREATE TABLE space_calendar_sources (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    connected_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider='google_calendar'),
    external_calendar_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 240),
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (char_length(timezone) BETWEEN 1 AND 80),
    sync_token TEXT NOT NULL DEFAULT '',
    watch_channel_id TEXT NOT NULL DEFAULT '',
    watch_resource_id TEXT NOT NULL DEFAULT '',
    watch_token_hash TEXT NOT NULL DEFAULT '',
    watch_expires_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','syncing','active','needs_attention','disabled')),
    last_error_code TEXT NOT NULL DEFAULT '',
    last_reconciled_at TIMESTAMPTZ,
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,integration_id,external_calendar_id)
);
CREATE INDEX space_calendar_sources_health_idx ON space_calendar_sources(status,watch_expires_at,last_reconciled_at);

CREATE TABLE space_calendar_events (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL REFERENCES space_calendar_sources(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider='google_calendar'),
    external_event_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    meeting_url TEXT NOT NULL DEFAULT '',
    organizer JSONB NOT NULL DEFAULT '{}'::jsonb,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    all_day BOOLEAN NOT NULL DEFAULT FALSE,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    status TEXT NOT NULL DEFAULT 'confirmed' CHECK (status IN ('confirmed','tentative','canceled')),
    provider_created_at TIMESTAMPTZ,
    provider_updated_at TIMESTAMPTZ,
    removed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_id,external_event_id),
    CHECK (ends_at >= starts_at)
);
CREATE INDEX space_calendar_events_range_idx ON space_calendar_events(space_id,starts_at,ends_at) WHERE removed_at IS NULL;

CREATE TABLE provider_shared_resources (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    published_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('slack','discord','notion')),
    resource_type TEXT NOT NULL CHECK (resource_type IN ('channel','page','database','data_source')),
    external_resource_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 240),
    permission_scope TEXT NOT NULL,
    configuration JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','needs_attention','disabled')),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,integration_id,provider,resource_type,external_resource_id)
);
CREATE INDEX provider_shared_resources_space_idx ON provider_shared_resources(space_id,provider,status,display_name);

CREATE TABLE provider_content_records (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    shared_resource_id TEXT NOT NULL REFERENCES provider_shared_resources(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('slack','discord','notion')),
    external_record_id TEXT NOT NULL,
    parent_external_id TEXT NOT NULL DEFAULT '',
    record_type TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT 'application/json',
    occurred_at TIMESTAMPTZ,
    content JSONB NOT NULL DEFAULT '{}'::jsonb,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(shared_resource_id,external_record_id)
);
CREATE INDEX provider_content_records_query_idx ON provider_content_records(space_id,provider,occurred_at DESC,id) WHERE deleted_at IS NULL;

CREATE TABLE provider_gateway_state (
    provider TEXT PRIMARY KEY CHECK (provider='discord'),
    session_id TEXT NOT NULL DEFAULT '',
    resume_url TEXT NOT NULL DEFAULT '',
    sequence BIGINT NOT NULL DEFAULT 0,
    last_heartbeat_at TIMESTAMPTZ,
    last_event_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'disconnected' CHECK (status IN ('connected','degraded','disconnected')),
    last_error_code TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE space_tasks ENABLE ROW LEVEL SECURITY; ALTER TABLE space_tasks FORCE ROW LEVEL SECURITY;
ALTER TABLE space_calendar_sources ENABLE ROW LEVEL SECURITY; ALTER TABLE space_calendar_sources FORCE ROW LEVEL SECURITY;
ALTER TABLE space_calendar_events ENABLE ROW LEVEL SECURITY; ALTER TABLE space_calendar_events FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_shared_resources ENABLE ROW LEVEL SECURITY; ALTER TABLE provider_shared_resources FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_content_records ENABLE ROW LEVEL SECURITY; ALTER TABLE provider_content_records FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_gateway_state ENABLE ROW LEVEL SECURITY; ALTER TABLE provider_gateway_state FORCE ROW LEVEL SECURITY;

CREATE POLICY space_tasks_member_policy ON space_tasks FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_calendar_sources_member_policy ON space_calendar_sources FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_calendar_events_member_policy ON space_calendar_events FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY provider_shared_resources_member_policy ON provider_shared_resources FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY provider_content_records_member_policy ON provider_content_records FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY provider_gateway_state_service_policy ON provider_gateway_state FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

UPDATE space_roles role SET permissions=(
    SELECT COALESCE(jsonb_agg(DISTINCT permission), '[]'::jsonb)
    FROM jsonb_array_elements(role.permissions || '["tasks.view","tasks.manage","integrations.manage"]'::jsonb) AS permission
) WHERE role.is_everyone;

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_tasks,space_calendar_sources,space_calendar_events,provider_shared_resources,provider_content_records,provider_gateway_state TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS provider_gateway_state,provider_content_records,provider_shared_resources,space_calendar_events,space_calendar_sources,space_tasks CASCADE;
UPDATE space_roles SET permissions=permissions-'tasks.view'-'tasks.manage'-'integrations.manage';
-- +goose StatementEnd
