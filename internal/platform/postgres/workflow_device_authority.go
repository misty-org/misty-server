package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Lock the run before its device job. Cancellation takes locks in the same order.
func deviceRunAuthorityTx(ctx context.Context, tx *sql.Tx, userID, runID string, expectedRuntime *string, capability string, allowWait ...bool) (string, *time.Time, error) {
	var state, spaceID, runtime string
	var payload json.RawMessage
	query := `SELECT state,COALESCE(space_id,''),COALESCE(runtime_run_id,''),input FROM space_runs WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`
	if strings.HasPrefix(runID, "invocation_") {
		query = `SELECT state,COALESCE(space_id,''),COALESCE(runtime_run_id,''),request_payload FROM ai_invocations WHERE id=$1 AND user_id=$2 AND COALESCE(agent_run_id,'')='' FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, query, runID, userID).Scan(&state, &spaceID, &runtime, &payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrSpaceForbidden
		}
		return "", nil, err
	}
	waiting := len(allowWait) > 0 && allowWait[0] && (state == "awaiting_approval" || state == "awaiting_device" || state == "awaiting_intervention")
	if state != "running" && !waiting || expectedRuntime != nil && runtime != *expectedRuntime {
		return "", nil, ErrSpaceForbidden
	}
	authority, err := AppAuthorityFromPayload(payload)
	if err != nil {
		return "", nil, err
	}
	scope := capability
	switch capability {
	case "browser.click", "browser.type", "browser.interact":
		scope = "browser.interact"
	case "browser.downloads.list":
		scope = "browser.inspect"
	case "browser.inspect", "browser.navigate", "files.read":
	default:
		if authority != nil {
			return "", nil, ErrAppRuntimeForbidden
		}
	}
	if err := validateAppExecutionAuthorityTx(ctx, tx, authority, userID, spaceID, "ai.write", scope); err != nil {
		return "", nil, err
	}
	if spaceID != "" {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return "", nil, err
		}
	}
	if runtime == "" || waiting {
		return runtime, nil, nil
	}
	budget, err := agentRunExecutionBudgetTx(ctx, tx, runID, true)
	if err != nil {
		return "", nil, err
	}
	return runtime, budget.Deadline, nil
}

func deviceJobDeadline(ctx context.Context, contextExpiry time.Time, budgetDeadline *time.Time) time.Time {
	deadline := time.Now().UTC().Add(5 * time.Minute)
	if contextExpiry.Before(deadline) {
		deadline = contextExpiry
	}
	if budgetDeadline != nil && budgetDeadline.Before(deadline) {
		deadline = *budgetDeadline
	}
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	return deadline
}

func validateDeviceJobTargetTx(ctx context.Context, tx *sql.Tx, job *WorkflowDeviceNodeJob, deviceID string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices d
 LEFT JOIN agent_run_contexts c ON c.id=$3 AND c.run_id=$4 AND c.owner_user_id=$1 AND c.device_id=d.id AND c.opaque_ref=$5 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? $6
 LEFT JOIN ai_invocation_contexts ac ON ac.id=$3 AND ac.invocation_id=$4 AND ac.user_id=$1 AND ac.device_id=d.id AND ac.opaque_ref=$5 AND ac.state='attached' AND ac.expires_at>NOW() AND ac.capabilities ? $6
 WHERE d.id=$2 AND d.user_id=$1 AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds' AND (c.id IS NOT NULL OR ac.id IS NOT NULL))`, job.UserID, deviceID, job.ContextID, job.RunID, job.ScopeID, job.RequiredCapability).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrSpaceForbidden
	}
	return nil
}
