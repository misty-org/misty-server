-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_roadmaps (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 5000),
    graph_version BIGINT NOT NULL DEFAULT 1 CHECK (graph_version > 0),
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(id,space_id)
);
CREATE INDEX space_roadmaps_space_idx ON space_roadmaps(space_id,updated_at DESC,id)
    WHERE archived_at IS NULL;

CREATE TABLE space_roadmap_milestones (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL,
    roadmap_id TEXT NOT NULL,
    title TEXT NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 10000),
    target_date DATE,
    rank BIGINT NOT NULL CHECK (rank > 0),
    position_x DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (abs(position_x) <= 10000000),
    position_y DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (abs(position_y) <= 10000000),
    width DOUBLE PRECISION NOT NULL DEFAULT 440 CHECK (width BETWEEN 280 AND 2400),
    height DOUBLE PRECISION NOT NULL DEFAULT 360 CHECK (height BETWEEN 220 AND 2400),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(id,roadmap_id,space_id),
    FOREIGN KEY(roadmap_id,space_id) REFERENCES space_roadmaps(id,space_id) ON DELETE CASCADE
);
CREATE INDEX space_roadmap_milestones_order_idx
    ON space_roadmap_milestones(roadmap_id,rank,id) WHERE archived_at IS NULL;

CREATE TABLE space_roadmap_goals (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL,
    roadmap_id TEXT NOT NULL,
    milestone_id TEXT NOT NULL,
    title TEXT NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 240),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    target_date DATE,
    rank BIGINT NOT NULL CHECK (rank > 0),
    position_x DOUBLE PRECISION NOT NULL DEFAULT 24 CHECK (abs(position_x) <= 10000000),
    position_y DOUBLE PRECISION NOT NULL DEFAULT 72 CHECK (abs(position_y) <= 10000000),
    manual_completed_at TIMESTAMPTZ,
    manual_completed_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(id,roadmap_id,space_id),
    FOREIGN KEY(roadmap_id,space_id) REFERENCES space_roadmaps(id,space_id) ON DELETE CASCADE,
    FOREIGN KEY(milestone_id,roadmap_id,space_id)
        REFERENCES space_roadmap_milestones(id,roadmap_id,space_id) ON DELETE CASCADE,
    CHECK ((manual_completed_at IS NULL) = (manual_completed_by_user_id IS NULL))
);
CREATE INDEX space_roadmap_goals_order_idx
    ON space_roadmap_goals(milestone_id,rank,id) WHERE archived_at IS NULL;
CREATE INDEX space_roadmap_goals_target_idx
    ON space_roadmap_goals(space_id,target_date,id)
    WHERE archived_at IS NULL AND target_date IS NOT NULL;

CREATE TABLE space_roadmap_edges (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL,
    roadmap_id TEXT NOT NULL,
    source_goal_id TEXT NOT NULL,
    target_goal_id TEXT NOT NULL,
    edge_type TEXT NOT NULL CHECK (edge_type IN ('dependency','related')),
    label TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 120),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(id,roadmap_id,space_id),
    UNIQUE(roadmap_id,source_goal_id,target_goal_id,edge_type),
    FOREIGN KEY(roadmap_id,space_id) REFERENCES space_roadmaps(id,space_id) ON DELETE CASCADE,
    FOREIGN KEY(source_goal_id,roadmap_id,space_id)
        REFERENCES space_roadmap_goals(id,roadmap_id,space_id) ON DELETE CASCADE,
    FOREIGN KEY(target_goal_id,roadmap_id,space_id)
        REFERENCES space_roadmap_goals(id,roadmap_id,space_id) ON DELETE CASCADE,
    CHECK (source_goal_id <> target_goal_id)
);
CREATE INDEX space_roadmap_edges_graph_idx
    ON space_roadmap_edges(roadmap_id,source_goal_id,target_goal_id);

ALTER TABLE space_tasks ADD CONSTRAINT space_tasks_id_space_unique UNIQUE(id,space_id);

CREATE TABLE space_roadmap_goal_tasks (
    space_id TEXT NOT NULL,
    roadmap_id TEXT NOT NULL,
    goal_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    added_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(goal_id,task_id),
    FOREIGN KEY(goal_id,roadmap_id,space_id)
        REFERENCES space_roadmap_goals(id,roadmap_id,space_id) ON DELETE CASCADE,
    FOREIGN KEY(task_id,space_id) REFERENCES space_tasks(id,space_id) ON DELETE CASCADE
);
CREATE INDEX space_roadmap_goal_tasks_task_idx ON space_roadmap_goal_tasks(task_id,goal_id);

CREATE OR REPLACE FUNCTION clear_roadmap_manual_completion_for_reopened_task()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
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
CREATE TRIGGER space_tasks_clear_roadmap_manual_completion
AFTER UPDATE OF status,archived_at ON space_tasks
FOR EACH ROW EXECUTE FUNCTION clear_roadmap_manual_completion_for_reopened_task();

ALTER TABLE space_roadmaps ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmaps FORCE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_milestones ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_milestones FORCE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_goals ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_goals FORCE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_edges FORCE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_goal_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_goal_tasks FORCE ROW LEVEL SECURITY;

CREATE POLICY space_roadmaps_member_policy ON space_roadmaps FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roadmap_milestones_member_policy ON space_roadmap_milestones FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roadmap_goals_member_policy ON space_roadmap_goals FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roadmap_edges_member_policy ON space_roadmap_edges FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roadmap_goal_tasks_member_policy ON space_roadmap_goal_tasks FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON
            space_roadmaps,space_roadmap_milestones,space_roadmap_goals,
            space_roadmap_edges,space_roadmap_goal_tasks TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS space_tasks_clear_roadmap_manual_completion ON space_tasks;
DROP FUNCTION IF EXISTS clear_roadmap_manual_completion_for_reopened_task();
DROP TABLE IF EXISTS space_roadmap_goal_tasks,space_roadmap_edges,space_roadmap_goals,
    space_roadmap_milestones,space_roadmaps CASCADE;
ALTER TABLE space_tasks DROP CONSTRAINT IF EXISTS space_tasks_id_space_unique;
-- +goose StatementEnd
