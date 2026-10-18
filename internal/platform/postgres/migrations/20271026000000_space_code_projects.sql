-- +goose Up
-- The Code section: coding projects people link to a Space. The files stay on
-- the owner's device; the Space keeps the name, the device and the GitHub
-- repository, so everyone can see what exists and where its changes go.
CREATE TABLE space_code_projects (
    id text PRIMARY KEY CHECK (id ~ '^codeproject_[0-9a-f-]{36}$'),
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The project's id on its device (an agent scope id).
    project_id text NOT NULL CHECK (char_length(project_id) BETWEEN 1 AND 200),
    device_id text NOT NULL DEFAULT '' CHECK (char_length(device_id) <= 200),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    -- owner/name on GitHub, or empty when the project has no remote.
    github_repo text NOT NULL DEFAULT '' CHECK (github_repo = '' OR github_repo ~ '^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$'),
    linked_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (space_id, owner_user_id, project_id),
    -- A person's projects leave a Space with them.
    FOREIGN KEY (space_id, owner_user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX space_code_projects_space ON space_code_projects(space_id, updated_at DESC);

ALTER TABLE space_code_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_code_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY space_code_projects_members ON space_code_projects
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_code_projects TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS space_code_projects;
