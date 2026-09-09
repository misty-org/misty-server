-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE space_runs
    ADD COLUMN device_wait_scope_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN device_wait_capability TEXT NOT NULL DEFAULT '',
    ADD COLUMN approval_wait_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_runtime_deliveries DROP CONSTRAINT agent_runtime_deliveries_operation_check,
    ADD CONSTRAINT agent_runtime_deliveries_operation_check CHECK(operation IN ('invocation.start','runtime.cancel','runtime.reconcile','approval.resume','device.resume'));
ALTER TABLE space_workflow_action_journal DROP CONSTRAINT space_workflow_action_journal_state_check,
    ADD CONSTRAINT space_workflow_action_journal_state_check CHECK(state IN ('started','completed','failed','unknown'));
UPDATE space_runs r SET approval_wait_id=COALESCE((SELECT a.id FROM agent_run_tool_approvals a
    WHERE a.run_id=r.id AND a.state IN ('pending','approved','denied','expired') ORDER BY a.expires_at DESC,a.id DESC LIMIT 1),'')
    WHERE r.state='awaiting_approval' OR r.runtime_phase='approval_resume_pending';
-- An exact command approval previously upgraded the whole run. Preserve modes
-- explicitly chosen at admission while retiring that unintended escalation.
UPDATE space_runs SET effective_run_mode=initial_run_mode
    WHERE effective_run_mode='full' AND initial_run_mode IN ('ask','auto')
      AND approval_state='approved' AND state IN ('queued','running','awaiting_approval','awaiting_device');
-- +goose StatementEnd

-- +goose Down
SELECT 1;
