-- +goose Up
-- Agents on devices (docs/design/agents-on-devices/BRIEF.md): a device proves a
-- tab is a session it opened for another device's request by the session job
-- that returned it. This finds those jobs quickly at claim time.
CREATE INDEX IF NOT EXISTS workflow_device_session_jobs_idx
    ON workflow_device_node_jobs (user_id, assigned_device_id, completed_at)
    WHERE state = 'completed' AND operation IN ('browser.session', 'browser.spawn_tabs');

-- +goose Down
DROP INDEX IF EXISTS workflow_device_session_jobs_idx;
