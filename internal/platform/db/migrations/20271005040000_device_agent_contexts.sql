-- +goose Up
-- A desktop chat can attach two more device grants besides a browser tab: a
-- folder the person shared with agents on that computer, and the Misty
-- browser workspace (open tabs and bookmarks).
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check
    CHECK (kind IN ('browser_tab', 'local_folder', 'workspace'));

-- +goose Down
DELETE FROM ai_invocation_contexts WHERE kind <> 'browser_tab';
ALTER TABLE ai_invocation_contexts DROP CONSTRAINT ai_invocation_contexts_kind_check;
ALTER TABLE ai_invocation_contexts ADD CONSTRAINT ai_invocation_contexts_kind_check CHECK (kind = 'browser_tab');
