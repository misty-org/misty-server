-- +goose Up
-- +goose StatementBegin
ALTER TABLE agent_attachments ADD COLUMN logical_document_id TEXT;
UPDATE agent_attachments
SET logical_document_id = 'document_' || replace(substring(id from 12), '-', '');
ALTER TABLE agent_attachments
    ALTER COLUMN logical_document_id SET NOT NULL,
    ADD CONSTRAINT agent_attachments_logical_document_id_check
        CHECK (logical_document_id ~ '^document_[0-9a-f]{32}$');
CREATE INDEX agent_attachments_job_document_idx
    ON agent_attachments(job_id, logical_document_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS agent_attachments_job_document_idx;
ALTER TABLE agent_attachments DROP COLUMN IF EXISTS logical_document_id;
-- +goose StatementEnd
