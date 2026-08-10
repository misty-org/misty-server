-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_roadmap_node_definitions (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    icon TEXT NOT NULL DEFAULT 'shapes' CHECK (char_length(icon) BETWEEN 1 AND 80),
    color TEXT NOT NULL DEFAULT 'slate' CHECK (color IN ('slate','blue','cyan','emerald','amber','orange','rose','violet')),
    agenda_visible BOOLEAN NOT NULL DEFAULT FALSE,
    field_schema JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(field_schema)='array'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(id,space_id)
);
CREATE INDEX space_roadmap_node_definitions_space_idx
    ON space_roadmap_node_definitions(space_id,name,id) WHERE archived_at IS NULL;

CREATE TABLE space_roadmap_nodes (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL,
    roadmap_id TEXT NOT NULL,
    milestone_id TEXT,
    definition_id TEXT,
    node_kind TEXT NOT NULL CHECK (node_kind IN ('risk','decision','metric','note','custom')),
    title TEXT NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 240),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    target_date DATE,
    position_x DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (abs(position_x) <= 10000000),
    position_y DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (abs(position_y) <= 10000000),
    field_values JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(field_values)='object'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(id,roadmap_id,space_id),
    FOREIGN KEY(roadmap_id,space_id) REFERENCES space_roadmaps(id,space_id) ON DELETE CASCADE,
    FOREIGN KEY(milestone_id,roadmap_id,space_id)
        REFERENCES space_roadmap_milestones(id,roadmap_id,space_id) ON DELETE CASCADE,
    FOREIGN KEY(definition_id,space_id)
        REFERENCES space_roadmap_node_definitions(id,space_id) ON DELETE RESTRICT,
    CHECK ((node_kind='custom')=(definition_id IS NOT NULL))
);
CREATE INDEX space_roadmap_nodes_graph_idx
    ON space_roadmap_nodes(roadmap_id,milestone_id,id) WHERE archived_at IS NULL;
CREATE INDEX space_roadmap_nodes_target_idx
    ON space_roadmap_nodes(space_id,target_date,id)
    WHERE archived_at IS NULL AND target_date IS NOT NULL;

ALTER TABLE space_roadmap_edges
    ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'goal',
    ADD COLUMN source_id TEXT,
    ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'goal',
    ADD COLUMN target_id TEXT;
UPDATE space_roadmap_edges
SET source_id=source_goal_id,target_id=target_goal_id;
ALTER TABLE space_roadmap_edges
    ALTER COLUMN source_id SET NOT NULL,
    ALTER COLUMN target_id SET NOT NULL,
    ALTER COLUMN source_goal_id DROP NOT NULL,
    ALTER COLUMN target_goal_id DROP NOT NULL;
ALTER TABLE space_roadmap_edges DROP CONSTRAINT space_roadmap_edges_edge_type_check;
ALTER TABLE space_roadmap_edges ADD CONSTRAINT space_roadmap_edges_edge_type_check
    CHECK (edge_type IN ('depends_on','dependency','blocks','enables','contributes_to','measures','documents','related'));
UPDATE space_roadmap_edges SET edge_type='depends_on' WHERE edge_type='dependency';
ALTER TABLE space_roadmap_edges ADD CONSTRAINT space_roadmap_edges_source_kind_check
    CHECK (source_kind IN ('milestone','goal','node'));
ALTER TABLE space_roadmap_edges ADD CONSTRAINT space_roadmap_edges_target_kind_check
    CHECK (target_kind IN ('milestone','goal','node'));
ALTER TABLE space_roadmap_edges ADD CONSTRAINT space_roadmap_edges_distinct_endpoints_check
    CHECK (source_kind<>target_kind OR source_id<>target_id);
CREATE UNIQUE INDEX space_roadmap_edges_endpoint_unique_idx
    ON space_roadmap_edges(roadmap_id,source_kind,source_id,target_kind,target_id,edge_type);

ALTER TABLE space_roadmap_node_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_node_definitions FORCE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_roadmap_nodes FORCE ROW LEVEL SECURITY;
CREATE POLICY space_roadmap_node_definitions_member_policy ON space_roadmap_node_definitions FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roadmap_nodes_member_policy ON space_roadmap_nodes FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON
            space_roadmap_node_definitions,space_roadmap_nodes TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_roadmap_edges_endpoint_unique_idx;
DELETE FROM space_roadmap_edges WHERE source_goal_id IS NULL OR target_goal_id IS NULL;
UPDATE space_roadmap_edges SET edge_type='dependency' WHERE edge_type='depends_on';
DELETE FROM space_roadmap_edges WHERE edge_type NOT IN ('dependency','related');
ALTER TABLE space_roadmap_edges DROP CONSTRAINT IF EXISTS space_roadmap_edges_distinct_endpoints_check;
ALTER TABLE space_roadmap_edges DROP CONSTRAINT IF EXISTS space_roadmap_edges_target_kind_check;
ALTER TABLE space_roadmap_edges DROP CONSTRAINT IF EXISTS space_roadmap_edges_source_kind_check;
ALTER TABLE space_roadmap_edges DROP CONSTRAINT IF EXISTS space_roadmap_edges_edge_type_check;
ALTER TABLE space_roadmap_edges ADD CONSTRAINT space_roadmap_edges_edge_type_check
    CHECK (edge_type IN ('dependency','related'));
ALTER TABLE space_roadmap_edges
    ALTER COLUMN source_goal_id SET NOT NULL,
    ALTER COLUMN target_goal_id SET NOT NULL,
    DROP COLUMN target_id,
    DROP COLUMN target_kind,
    DROP COLUMN source_id,
    DROP COLUMN source_kind;
DROP TABLE IF EXISTS space_roadmap_nodes,space_roadmap_node_definitions CASCADE;
-- +goose StatementEnd
