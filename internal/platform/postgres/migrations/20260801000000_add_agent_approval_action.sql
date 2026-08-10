-- +goose Up
-- +goose StatementBegin
-- Some early dogfood databases created agent_approvals before the typed action
-- payload was added to the original agents migration. Preserve their history,
-- but make legacy pending approvals non-actionable because their exact payload
-- cannot be reconstructed safely from the digest alone.
ALTER TABLE agent_approvals
    ADD COLUMN IF NOT EXISTS action JSONB;

UPDATE agent_approvals
SET action = jsonb_build_object(
    'kind', action_kind,
    'summary', action_summary,
    'scopeId', 'legacy_unavailable'
)
WHERE action IS NULL;

WITH legacy_pending AS (
    UPDATE agent_approvals
    SET state = 'expired', decided_at = COALESCE(decided_at, NOW())
    WHERE state = 'pending'
      AND action->>'scopeId' = 'legacy_unavailable'
    RETURNING job_id
)
UPDATE agent_jobs
SET state = 'canceled',
    canceled_at = COALESCE(canceled_at, NOW()),
    updated_at = NOW()
WHERE id IN (SELECT job_id FROM legacy_pending)
  AND state = 'awaiting_approval';

ALTER TABLE agent_approvals
    ALTER COLUMN action SET NOT NULL,
    ADD CONSTRAINT agent_approvals_action_object_check
        CHECK (jsonb_typeof(action) = 'object');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_approvals
    DROP CONSTRAINT IF EXISTS agent_approvals_action_object_check,
    DROP COLUMN IF EXISTS action;
-- +goose StatementEnd
