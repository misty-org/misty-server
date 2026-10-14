-- +goose Up
-- Code projects (docs/plans/agent-code-projects.md): a chat may attach a
-- folder the person shared with write access on their own computer. Its
-- capabilities are the code.* tools, checked again on the device.
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace', 'inbox', 'project_folder'));

-- +goose Down
DELETE FROM ai_invocation_contexts WHERE kind = 'project_folder';
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace', 'inbox'));
