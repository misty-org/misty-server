-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- One reusable audience predicate for every Space resource. Conversation
-- membership is deliberately dynamic: newly-added people gain the history and
-- removed people lose it immediately. Space ownership is not an override.
CREATE OR REPLACE FUNCTION misty_can_access_space_audience(
    candidate_space_id TEXT,
    candidate_audience_kind TEXT,
    candidate_conversation_id TEXT
) RETURNS BOOLEAN
LANGUAGE SQL STABLE SECURITY DEFINER
SET search_path = public, pg_temp SET row_security = off AS $$
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

-- Agent runs no longer overload source_conversation_id with an Everyone
-- message ID. New code uses these typed, immutable scope fields.
ALTER TABLE space_runs
    ADD COLUMN conversation_scope_kind TEXT NOT NULL DEFAULT 'everyone'
        CHECK(conversation_scope_kind IN ('everyone','conversation')),
    ADD COLUMN scope_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE SET NULL,
    ADD COLUMN source_message_id TEXT REFERENCES space_messages(id) ON DELETE SET NULL,
    ADD CONSTRAINT space_runs_conversation_scope_check CHECK(
        (conversation_scope_kind='everyone' AND scope_conversation_id IS NULL) OR
        (conversation_scope_kind='conversation' AND scope_conversation_id IS NOT NULL)
    );
UPDATE space_runs r SET conversation_scope_kind='conversation',scope_conversation_id=c.id
FROM space_conversations c
WHERE c.id=r.source_conversation_id AND c.space_id=r.space_id;
UPDATE space_runs r SET source_message_id=m.id
FROM space_messages m
WHERE m.id=r.source_conversation_id AND m.space_id=r.space_id;
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_source_type_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_source_type_check CHECK(source_type IN (
    'direct','group_mention','agent_console','studio_test','schedule','connector','task','suggestion','follow_up'
));
CREATE INDEX space_runs_scope_idx
    ON space_runs(space_id,conversation_scope_kind,scope_conversation_id,created_at DESC);

-- Existing rows remain Space-wide. Only newly-created, conversation-derived
-- resources use the private audience.
ALTER TABLE space_tasks
    ADD COLUMN audience_kind TEXT NOT NULL DEFAULT 'space' CHECK(audience_kind IN ('space','conversation')),
    ADD COLUMN audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    ADD COLUMN audience_creator_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
    ADD CONSTRAINT space_tasks_audience_check CHECK(
        (audience_kind='space' AND audience_conversation_id IS NULL) OR
        (audience_kind='conversation' AND audience_conversation_id IS NOT NULL AND audience_creator_user_id IS NOT NULL)
    );
ALTER TABLE space_notes
    ADD COLUMN audience_kind TEXT NOT NULL DEFAULT 'space' CHECK(audience_kind IN ('space','conversation')),
    ADD COLUMN audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    ADD CONSTRAINT space_notes_audience_check CHECK(
        (audience_kind='space' AND audience_conversation_id IS NULL) OR
        (audience_kind='conversation' AND audience_conversation_id IS NOT NULL)
    );
ALTER TABLE space_drawings
    ADD COLUMN audience_kind TEXT NOT NULL DEFAULT 'space' CHECK(audience_kind IN ('space','conversation')),
    ADD COLUMN audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    ADD CONSTRAINT space_drawings_audience_check CHECK(
        (audience_kind='space' AND audience_conversation_id IS NULL) OR
        (audience_kind='conversation' AND audience_conversation_id IS NOT NULL)
    );
ALTER TABLE space_roadmaps
    ADD COLUMN audience_kind TEXT NOT NULL DEFAULT 'space' CHECK(audience_kind IN ('space','conversation')),
    ADD COLUMN audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    ADD CONSTRAINT space_roadmaps_audience_check CHECK(
        (audience_kind='space' AND audience_conversation_id IS NULL) OR
        (audience_kind='conversation' AND audience_conversation_id IS NOT NULL)
    );
ALTER TABLE space_library_items
    ADD COLUMN audience_kind TEXT NOT NULL DEFAULT 'space' CHECK(audience_kind IN ('space','conversation')),
    ADD COLUMN audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    ADD CONSTRAINT space_library_items_audience_check CHECK(
        (audience_kind='space' AND audience_conversation_id IS NULL) OR
        (audience_kind='conversation' AND audience_conversation_id IS NOT NULL)
    );

DROP POLICY IF EXISTS space_tasks_member_policy ON space_tasks;
CREATE POLICY space_tasks_audience_policy ON space_tasks FOR ALL
    USING(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id))
    WITH CHECK(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id));
DROP POLICY IF EXISTS space_notes_access_policy ON space_notes;
CREATE POLICY space_notes_audience_policy ON space_notes FOR ALL
    USING(misty_rls_is_service() OR (lifecycle_state='active' AND misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id)))
    WITH CHECK(misty_rls_is_service() OR (creator_user_id=misty_rls_user_id() AND misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id)));
DROP POLICY IF EXISTS space_drawings_access_policy ON space_drawings;
CREATE POLICY space_drawings_audience_policy ON space_drawings FOR ALL
    USING(misty_rls_is_service() OR (lifecycle_state='active' AND misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id)))
    WITH CHECK(misty_rls_is_service() OR (creator_user_id=misty_rls_user_id() AND misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id)));
DROP POLICY IF EXISTS space_roadmaps_member_policy ON space_roadmaps;
CREATE POLICY space_roadmaps_audience_policy ON space_roadmaps FOR ALL
    USING(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id))
    WITH CHECK(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id));
DROP POLICY IF EXISTS space_library_items_policy ON space_library_items;
CREATE POLICY space_library_items_audience_policy ON space_library_items FOR ALL
    USING(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id))
    WITH CHECK(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id));

-- Misty-native calendar events are first-class events, never Tasks. Connected
-- provider rows stay in space_calendar_events as their read-through cache.
CREATE TABLE space_native_calendar_events (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK(char_length(btrim(title)) BETWEEN 1 AND 240),
    description TEXT NOT NULL DEFAULT '' CHECK(char_length(description)<=20000),
    location TEXT NOT NULL DEFAULT '' CHECK(char_length(location)<=1000),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    all_day BOOLEAN NOT NULL DEFAULT FALSE,
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK(char_length(timezone) BETWEEN 1 AND 80),
    status TEXT NOT NULL DEFAULT 'confirmed' CHECK(status IN ('confirmed','tentative','canceled')),
    audience_kind TEXT NOT NULL DEFAULT 'space' CHECK(audience_kind IN ('space','conversation')),
    audience_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_by_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL,
    source_run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK(ends_at>=starts_at),
    CHECK(
        (audience_kind='space' AND audience_conversation_id IS NULL) OR
        (audience_kind='conversation' AND audience_conversation_id IS NOT NULL)
    )
);
CREATE INDEX space_native_calendar_events_range_idx
    ON space_native_calendar_events(space_id,starts_at,ends_at) WHERE archived_at IS NULL;
ALTER TABLE space_native_calendar_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_native_calendar_events FORCE ROW LEVEL SECURITY;
CREATE POLICY space_native_calendar_events_audience_policy ON space_native_calendar_events FOR ALL
    USING(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id))
    WITH CHECK(misty_rls_is_service() OR misty_can_access_space_audience(space_id,audience_kind,audience_conversation_id));

CREATE TABLE space_action_suggestion_settings (
    space_id TEXT PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    weekly_limit INTEGER NOT NULL DEFAULT 100 CHECK(weekly_limit BETWEEN 0 AND 10000),
    weekly_used INTEGER NOT NULL DEFAULT 0 CHECK(weekly_used>=0),
    reset_at TIMESTAMPTZ NOT NULL DEFAULT date_trunc('week',NOW())+INTERVAL '1 week',
    updated_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE space_conversation_suggestion_vetoes (
    conversation_id TEXT NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(conversation_id,user_id)
);
CREATE TABLE space_action_suggestion_jobs (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    scope_kind TEXT NOT NULL CHECK(scope_kind IN ('everyone','conversation')),
    conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    anchor_message_id TEXT NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','working','completed','skipped','failed')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(anchor_message_id),
    CHECK((scope_kind='everyone' AND conversation_id IS NULL) OR (scope_kind='conversation' AND conversation_id IS NOT NULL))
);
CREATE INDEX space_action_suggestion_jobs_due_idx
    ON space_action_suggestion_jobs(available_at,created_at) WHERE state='queued';
CREATE TABLE space_action_suggestion_batches (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    scope_kind TEXT NOT NULL CHECK(scope_kind IN ('everyone','conversation')),
    conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    anchor_message_id TEXT NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(evidence)='array'),
    fingerprint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','partial','resolved','invalidated','expired')),
    version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '7 days',
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,fingerprint),
    CHECK((scope_kind='everyone' AND conversation_id IS NULL) OR (scope_kind='conversation' AND conversation_id IS NOT NULL))
);
CREATE TABLE space_action_suggestion_items (
    id TEXT PRIMARY KEY,
    batch_id TEXT NOT NULL REFERENCES space_action_suggestion_batches(id) ON DELETE CASCADE,
    action_kind TEXT NOT NULL CHECK(action_kind IN ('task.create','calendar.event.create','journal.note.create','roadmap.item.create','conversation.follow_up.schedule')),
    title TEXT NOT NULL CHECK(char_length(btrim(title)) BETWEEN 1 AND 240),
    summary TEXT NOT NULL DEFAULT '' CHECK(char_length(summary)<=2000),
    proposed_input JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(proposed_input)='object'),
    approved_input JSONB CHECK(approved_input IS NULL OR jsonb_typeof(approved_input)='object'),
    required_capability TEXT NOT NULL,
    selected_agent_id TEXT REFERENCES personal_agents(id) ON DELETE SET NULL,
    accepted_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    follow_up_id TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','accepted','completed','failed','canceled','invalidated')),
    ordinal SMALLINT NOT NULL CHECK(ordinal BETWEEN 0 AND 2),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(batch_id,ordinal)
);
CREATE TABLE space_action_suggestion_dismissals (
    batch_id TEXT NOT NULL REFERENCES space_action_suggestion_batches(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(batch_id,user_id)
);
CREATE TABLE space_conversation_follow_ups (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    source_scope_kind TEXT NOT NULL CHECK(source_scope_kind IN ('everyone','conversation')),
    source_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    source_message_id TEXT REFERENCES space_messages(id) ON DELETE SET NULL,
    suggestion_item_id TEXT UNIQUE REFERENCES space_action_suggestion_items(id) ON DELETE SET NULL,
    authorizing_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL REFERENCES personal_agents(id) ON DELETE RESTRICT,
    reminder_text TEXT NOT NULL CHECK(char_length(btrim(reminder_text)) BETWEEN 1 AND 4000),
    deliver_at TIMESTAMPTZ NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','working','delivered','partially_delivered','canceled','failed')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK((source_scope_kind='everyone' AND source_conversation_id IS NULL) OR (source_scope_kind='conversation' AND source_conversation_id IS NOT NULL))
);
ALTER TABLE space_action_suggestion_items
    ADD CONSTRAINT space_action_suggestion_items_follow_up_fk
    FOREIGN KEY(follow_up_id) REFERENCES space_conversation_follow_ups(id) ON DELETE SET NULL;
CREATE TABLE space_conversation_follow_up_recipients (
    follow_up_id TEXT NOT NULL REFERENCES space_conversation_follow_ups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','delivered','skipped','opted_out','failed')),
    direct_conversation_id TEXT REFERENCES space_conversations(id) ON DELETE SET NULL,
    delivered_message_id TEXT REFERENCES space_messages(id) ON DELETE SET NULL,
    error_code TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(follow_up_id,user_id)
);
CREATE INDEX space_conversation_follow_ups_due_idx
    ON space_conversation_follow_ups(deliver_at,created_at) WHERE state='queued';

ALTER TABLE space_action_suggestion_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_suggestion_vetoes ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_dismissals ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_follow_ups ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_follow_up_recipients ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_settings FORCE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_jobs FORCE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_suggestion_vetoes FORCE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_batches FORCE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_items FORCE ROW LEVEL SECURITY;
ALTER TABLE space_action_suggestion_dismissals FORCE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_follow_ups FORCE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_follow_up_recipients FORCE ROW LEVEL SECURITY;
CREATE POLICY space_action_suggestion_settings_member ON space_action_suggestion_settings FOR SELECT
    USING(misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_action_suggestion_settings_owner ON space_action_suggestion_settings FOR ALL
    USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=space_id AND s.owner_user_id=misty_rls_user_id()))
    WITH CHECK(misty_rls_is_service() OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=space_id AND s.owner_user_id=misty_rls_user_id()));
CREATE POLICY space_action_suggestion_jobs_service ON space_action_suggestion_jobs FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
CREATE POLICY space_conversation_suggestion_vetoes_member ON space_conversation_suggestion_vetoes FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_conversation_member(conversation_id)));
CREATE POLICY space_action_suggestion_batches_audience ON space_action_suggestion_batches FOR SELECT
    USING(misty_rls_is_service() OR misty_can_access_space_audience(space_id,CASE WHEN scope_kind='everyone' THEN 'space' ELSE 'conversation' END,conversation_id));
CREATE POLICY space_action_suggestion_items_audience ON space_action_suggestion_items FOR SELECT
    USING(misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_action_suggestion_batches b WHERE b.id=batch_id));
CREATE POLICY space_action_suggestion_dismissals_owner ON space_action_suggestion_dismissals FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY space_conversation_follow_ups_audience ON space_conversation_follow_ups FOR SELECT
    USING(misty_rls_is_service() OR (source_scope_kind='everyone' AND misty_is_space_member(space_id)) OR (source_scope_kind='conversation' AND misty_is_space_conversation_member(source_conversation_id)));
CREATE POLICY space_conversation_follow_up_recipients_audience ON space_conversation_follow_up_recipients FOR SELECT
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id() OR EXISTS(SELECT 1 FROM space_conversation_follow_ups f WHERE f.id=follow_up_id AND f.authorizing_user_id=misty_rls_user_id()));

DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON
            space_native_calendar_events,space_action_suggestion_settings,
            space_conversation_suggestion_vetoes,space_action_suggestion_jobs,
            space_action_suggestion_batches,space_action_suggestion_items,
            space_action_suggestion_dismissals,space_conversation_follow_ups,
            space_conversation_follow_up_recipients TO misty_app;
        GRANT EXECUTE ON FUNCTION misty_can_access_space_audience(TEXT,TEXT,TEXT) TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_conversation_follow_up_recipients;
ALTER TABLE space_action_suggestion_items DROP CONSTRAINT IF EXISTS space_action_suggestion_items_follow_up_fk;
DROP TABLE IF EXISTS space_conversation_follow_ups;
DROP TABLE IF EXISTS space_action_suggestion_dismissals;
DROP TABLE IF EXISTS space_action_suggestion_items;
DROP TABLE IF EXISTS space_action_suggestion_batches;
DROP TABLE IF EXISTS space_action_suggestion_jobs;
DROP TABLE IF EXISTS space_conversation_suggestion_vetoes;
DROP TABLE IF EXISTS space_action_suggestion_settings;
DROP TABLE IF EXISTS space_native_calendar_events;
DROP POLICY IF EXISTS space_library_items_audience_policy ON space_library_items;
DROP POLICY IF EXISTS space_roadmaps_audience_policy ON space_roadmaps;
DROP POLICY IF EXISTS space_drawings_audience_policy ON space_drawings;
DROP POLICY IF EXISTS space_notes_audience_policy ON space_notes;
DROP POLICY IF EXISTS space_tasks_audience_policy ON space_tasks;
ALTER TABLE space_library_items DROP COLUMN IF EXISTS audience_conversation_id,DROP COLUMN IF EXISTS audience_kind;
ALTER TABLE space_roadmaps DROP COLUMN IF EXISTS audience_conversation_id,DROP COLUMN IF EXISTS audience_kind;
ALTER TABLE space_drawings DROP COLUMN IF EXISTS audience_conversation_id,DROP COLUMN IF EXISTS audience_kind;
ALTER TABLE space_notes DROP COLUMN IF EXISTS audience_conversation_id,DROP COLUMN IF EXISTS audience_kind;
ALTER TABLE space_tasks DROP COLUMN IF EXISTS audience_creator_user_id,DROP COLUMN IF EXISTS audience_conversation_id,DROP COLUMN IF EXISTS audience_kind;
CREATE POLICY space_library_items_policy ON space_library_items FOR ALL
    USING(misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK(misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roadmaps_member_policy ON space_roadmaps FOR ALL
    USING(misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK(misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_drawings_access_policy ON space_drawings FOR ALL
    USING(misty_rls_is_service() OR (lifecycle_state='active' AND misty_is_space_member(space_id)))
    WITH CHECK(misty_rls_is_service() OR (creator_user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));
CREATE POLICY space_notes_access_policy ON space_notes FOR ALL
    USING(misty_rls_is_service() OR (lifecycle_state='active' AND (creator_user_id=misty_rls_user_id() OR EXISTS(SELECT 1 FROM space_note_permissions p JOIN space_members m ON m.space_id=space_notes.space_id AND m.user_id=p.user_id WHERE p.note_id=space_notes.id AND p.user_id=misty_rls_user_id()))))
    WITH CHECK(misty_rls_is_service() OR creator_user_id=misty_rls_user_id());
CREATE POLICY space_tasks_member_policy ON space_tasks FOR ALL
    USING(misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK(misty_rls_is_service() OR misty_is_space_member(space_id));
DROP INDEX IF EXISTS space_runs_scope_idx;
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_conversation_scope_check;
ALTER TABLE space_runs DROP COLUMN IF EXISTS source_message_id,DROP COLUMN IF EXISTS scope_conversation_id,DROP COLUMN IF EXISTS conversation_scope_kind;
ALTER TABLE space_runs DROP CONSTRAINT IF EXISTS space_runs_source_type_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_source_type_check CHECK(source_type IN (
    'direct','group_mention','agent_console','studio_test','schedule','connector','task'
));
DROP FUNCTION IF EXISTS misty_can_access_space_audience(TEXT,TEXT,TEXT);
-- +goose StatementEnd
