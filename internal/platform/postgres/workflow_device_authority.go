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
	if err := validateNativeAgentExecutionTx(ctx, tx, userID, spaceID, payload); err != nil {
		return "", nil, err
	}
	authority, err := AppAuthorityFromPayload(payload)
	if err != nil {
		return "", nil, err
	}
	scope := capability
	switch capability {
	case "browser.click", "browser.type", "browser.interact", "browser.upload":
		scope = "browser.interact"
	case "browser.downloads.list", "browser.visual":
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
	// Revalidate the signed device, attached context and originating window.
	// Native browser tools do not depend on the retired app-install platform.
	if strings.HasPrefix(job.RunID, "invocation_") {
		var payload json.RawMessage
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT request_payload,COALESCE(space_id,'') FROM ai_invocations WHERE id=$1 AND user_id=$2`, job.RunID, job.UserID).Scan(&payload, &spaceID); err != nil {
			return err
		}
		var input struct {
			AgentID     string `json:"agent_id"`
			Mode        string `json:"execution_mode"`
			WindowLabel string `json:"window_label"`
		}
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
		if input.AgentID != "" {
			if err := validateNativeAgentExecutionTx(ctx, tx, job.UserID, spaceID, payload); err != nil {
				return err
			}
			if input.Mode == "user" && job.RequiredCapability != "browser.inspect" && job.RequiredCapability != "browser.visual" && job.RequiredCapability != "browser.downloads.list" {
				return ErrSpaceForbidden
			}
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocation_contexts c WHERE c.id=$1 AND c.invocation_id=$2 AND c.user_id=$3 AND c.metadata->>'window_label'=$4)`, job.ContextID, job.RunID, job.UserID, input.WindowLabel).Scan(&valid)
			if err != nil {
				return err
			}
			if !valid {
				return ErrSpaceForbidden
			}
			if job.Operation == "browser.upload" {
				var upload struct {
					DownloadID    string `json:"downloadId"`
					SourceScopeID string `json:"sourceScopeId"`
				}
				if err := json.Unmarshal(job.Input, &upload); err != nil {
					return err
				}
				if upload.DownloadID != "" {
					// Recheck the source as well as the destination on claim/begin/
					// lease renewal. Detaching either context stops the transfer.
					err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocation_contexts c
  WHERE c.invocation_id=$1 AND c.user_id=$2 AND c.opaque_ref=$3 AND c.device_id=$4 AND c.state='attached' AND c.expires_at>NOW()
 AND c.capabilities ? 'browser.downloads.list' AND c.metadata->>'window_label'=$5)`, job.RunID, job.UserID, upload.SourceScopeID, deviceID, input.WindowLabel).Scan(&valid)
					if err != nil {
						return err
					}
					if !valid {
						return ErrSpaceForbidden
					}
				}
			}
		}
	}
	return nil
}
