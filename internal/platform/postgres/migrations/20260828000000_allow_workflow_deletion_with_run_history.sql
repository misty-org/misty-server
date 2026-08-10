-- +goose Up
ALTER TABLE space_runs
    DROP CONSTRAINT IF EXISTS space_runs_workflow_version_id_fkey;

ALTER TABLE space_runs
    ADD CONSTRAINT space_runs_workflow_version_id_fkey
    FOREIGN KEY (workflow_version_id)
    REFERENCES space_workflow_versions(id)
    ON DELETE SET NULL;

-- +goose Down
ALTER TABLE space_runs
    DROP CONSTRAINT IF EXISTS space_runs_workflow_version_id_fkey;

ALTER TABLE space_runs
    ADD CONSTRAINT space_runs_workflow_version_id_fkey
    FOREIGN KEY (workflow_version_id)
    REFERENCES space_workflow_versions(id)
    ON DELETE RESTRICT;
