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
			// A session this device opened for another device's request has its
			// own live task here; its tabs need neither the asking window nor an
			// agent-mode request.
			remote := false
			if strings.HasPrefix(job.RequiredCapability, "browser.") {
				if remote, err = remoteSessionScopeTx(ctx, tx, job.UserID, deviceID, job.ScopeID); err != nil {
					return err
				}
			}
			// Acting in a page needs a live agent task. Folders, tabs and
			// bookmarks follow their grants instead.
			if input.Mode == "user" && strings.HasPrefix(job.RequiredCapability, "browser.") && job.RequiredCapability != "browser.session" && !userModeBrowserCapability[job.RequiredCapability] && !remote {
				return ErrSpaceForbidden
			}
			// A browser tab belongs to the window that started the request.
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocation_contexts c WHERE c.id=$1 AND c.invocation_id=$2 AND c.user_id=$3
 AND (c.kind<>'browser_tab' OR $5 OR c.metadata->>'window_label'=$4))`, job.ContextID, job.RunID, job.UserID, input.WindowLabel, remote).Scan(&valid)
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

// remoteSessionScopeTx reports whether a scope is a tab this device opened for
// another device's request (docs/design/agents-on-devices/BRIEF.md): returned
// by a completed browser.session job on this device, or by a sub-agent's
// browser.spawn_tabs job whose parent was such a tab. The proof is the job's
// stored output, never anything the request says about the tab.
func remoteSessionScopeTx(ctx context.Context, tx *sql.Tx, userID, deviceID, scope string) (bool, error) {
	var remote bool
	err := tx.QueryRowContext(ctx, `WITH sessions AS (
 SELECT tab->>'opaque_ref' AS scope FROM workflow_device_node_jobs s, jsonb_array_elements(COALESCE(s.output->'tabs','[]'::jsonb)) tab
 WHERE s.user_id=$1 AND s.assigned_device_id=$2 AND s.state='completed' AND s.operation='browser.session' AND s.completed_at>NOW()-INTERVAL '24 hours')
SELECT EXISTS(SELECT 1 FROM sessions WHERE scope=$3) OR EXISTS(
 SELECT 1 FROM workflow_device_node_jobs c, jsonb_array_elements(COALESCE(c.output->'tabs','[]'::jsonb)) tab
 WHERE c.user_id=$1 AND c.assigned_device_id=$2 AND c.state='completed' AND c.operation='browser.spawn_tabs' AND c.completed_at>NOW()-INTERVAL '24 hours'
 AND tab->>'opaque_ref'=$3 AND c.scope_id IN (SELECT scope FROM sessions))`, userID, deviceID, scope).Scan(&remote)
	return remote, err
}

// userModeBrowserCapability lists the read-only browser operations a run in
// user mode may use; acting stays with agent mode.
var userModeBrowserCapability = map[string]bool{
	"browser.inspect": true, "browser.visual": true, "browser.downloads.list": true,
	"browser.snapshot": true, "browser.find": true, "browser.extract": true, "browser.wait_for": true,
}
