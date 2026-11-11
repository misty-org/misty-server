-- +goose Up
-- A Space's planner: the tasks on its board and the events on its calendar. Both are Space
-- content like notes and files: every member reads and changes them, and the server can read them.
-- A task is assigned to a member or to an agent a member brought, which the Space knows by its id.
CREATE TABLE space_tasks (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  task_number bigint NOT NULL,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 240),
  notes text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 20000),
  status text NOT NULL CHECK (status IN ('todo', 'in_progress', 'done', 'canceled')),
  priority text NOT NULL CHECK (priority IN ('high', 'medium', 'low')),
  rank bigint NOT NULL,
  assignee_user_id text REFERENCES users(id) ON DELETE SET NULL,
  assignee_agent_id text NOT NULL DEFAULT '' CHECK (char_length(assignee_agent_id) <= 200),
  due_at timestamptz,
  created_by_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  version bigint NOT NULL DEFAULT 1,
  completed_at timestamptz,
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (space_id, task_number),
  CHECK (assignee_user_id IS NULL OR assignee_agent_id = '')
);
CREATE INDEX space_tasks_board_idx ON space_tasks (space_id, status, rank) WHERE archived_at IS NULL;
CREATE INDEX space_tasks_assignee_idx ON space_tasks (assignee_user_id) WHERE assignee_user_id IS NOT NULL;
CREATE INDEX space_tasks_creator_idx ON space_tasks (created_by_user_id);

CREATE TABLE space_calendar_events (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 240),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
  location text NOT NULL DEFAULT '' CHECK (char_length(location) <= 500),
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  all_day boolean NOT NULL DEFAULT false,
  timezone text NOT NULL DEFAULT 'UTC' CHECK (char_length(timezone) BETWEEN 1 AND 100),
  created_by_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (ends_at >= starts_at)
);
CREATE INDEX space_calendar_events_range_idx ON space_calendar_events (space_id, starts_at, ends_at);
CREATE INDEX space_calendar_events_creator_idx ON space_calendar_events (created_by_user_id);

ALTER TABLE space_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY space_tasks_members ON space_tasks
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));
ALTER TABLE space_calendar_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_calendar_events FORCE ROW LEVEL SECURITY;
CREATE POLICY space_calendar_events_members ON space_calendar_events
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_tasks, space_calendar_events TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS space_calendar_events;
DROP TABLE IF EXISTS space_tasks;
