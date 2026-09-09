package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const sdkApprovalColumns = `id,invocation_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token,summary,state,expires_at,decided_at`

func (db *Database) RequireSDKToolApproval(ctx context.Context, userID, invocationID, effectID, toolName, argumentsHash, hookToken, summary string, reviews ...ProtectedSDKApproval) (*AgentToolApproval, bool, error) {
	if hookToken == "" || len(hookToken) > 500 {
		return nil, false, ErrSpaceInvalid
	}
	var result AgentToolApproval
	allowed := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT i.state FROM ai_invocations i LEFT JOIN sdk_capability_invocations c ON c.invocation_id=i.id WHERE i.id=$1 AND i.user_id=$2 AND COALESCE(i.agent_run_id,'')='' AND ((i.surface_id='sdk' AND c.effect_id=$3 AND c.cancel_requested_at IS NULL) OR (i.surface_id<>'sdk' AND EXISTS(SELECT 1 FROM agent_sdk_capability_bindings b WHERE b.ai_invocation_id=i.id AND b.user_id=i.user_id))) FOR UPDATE OF i`, invocationID, userID, effectID).Scan(&state); err != nil {
			return err
		}
		if state != "running" && state != "awaiting_approval" {
			return ErrSpaceConflict
		}
		err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT `+sdkApprovalColumns+` FROM agent_run_tool_approvals WHERE invocation_id=$1 AND tool_call_id=$2`, invocationID, effectID), &result)
		if err == nil {
			if result.ToolName != toolName || result.ArgumentsHash != argumentsHash {
				return ErrAppRuntimeForbidden
			}
			allowed = result.State == "approved"
			return persistSDKApprovalReviewTx(ctx, tx, &result, reviews)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state != "running" {
			return ErrSpaceConflict
		}
		err = scanAgentToolApproval(tx.QueryRowContext(ctx, `INSERT INTO agent_run_tool_approvals(id,invocation_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token,summary,expires_at)
   SELECT $1,$2,$3,$4,$5,'consequential',$6,$6,$7,$8,LEAST(expires_at,NOW()+INTERVAL '24 hours') FROM ai_invocations WHERE id=$2 RETURNING `+sdkApprovalColumns, uuid.NewString(), invocationID, userID, effectID, toolName, argumentsHash, hookToken, summary), &result)
		if err != nil {
			return err
		}
		if err := persistSDKApprovalReviewTx(ctx, tx, &result, reviews); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE ai_invocations SET state='awaiting_approval',approval_wait_id=$2,updated_at=NOW() WHERE id=$1`, invocationID, result.ID)
		return err
	})
	return &result, allowed, err
}
func (db *Database) DecideSDKToolApproval(ctx context.Context, userID, invocationID, approvalID string, approved bool) error {
	if AppAuthorityFromContext(ctx) != nil {
		return ErrAppRuntimeForbidden
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var runtimeID string
		if err := tx.QueryRowContext(ctx, `SELECT i.runtime_run_id FROM ai_invocations i LEFT JOIN sdk_capability_invocations c ON c.invocation_id=i.id WHERE i.id=$1 AND i.user_id=$2 AND i.approval_wait_id=$3 AND i.state IN ('awaiting_approval','running') AND (c.invocation_id IS NULL OR c.cancel_requested_at IS NULL) AND COALESCE(i.agent_run_id,'')='' FOR UPDATE OF i`, invocationID, userID, approvalID).Scan(&runtimeID); err != nil {
			return err
		}
		var approval AgentToolApproval
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT `+sdkApprovalColumns+` FROM agent_run_tool_approvals WHERE id=$1 AND invocation_id=$2 AND owner_user_id=$3 FOR UPDATE`, approvalID, invocationID, userID), &approval); err != nil {
			return err
		}
		state := "denied"
		if approved {
			state = "approved"
		}
		if approval.State != "pending" && approval.State != state {
			return ErrSpaceConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state=$2,decided_by_user_id=$3,decided_at=COALESCE(decided_at,NOW()) WHERE id=$1 AND expires_at>NOW() AND ($2<>'approved' OR (tool_name NOT LIKE 'sdk.%' AND tool_name NOT IN ('browser.click','browser.interact')) OR sdk_review_ciphertext IS NOT NULL)`, approvalID, state, userID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrSpaceConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='running',updated_at=NOW() WHERE id=$1`, invocationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
			return err
		}
		return queueAgentContinuationTx(ctx, tx, userID, invocationID, "approval.resume", approval.ID, AgentContinuation{RuntimeID: runtimeID, HookToken: approval.HookToken, ApprovalID: approval.ID, Approved: approved})
	})
}
func (db *Database) ExpireSDKToolApprovals(ctx context.Context) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.invocation_id,a.owner_user_id,a.hook_token,i.runtime_run_id FROM agent_run_tool_approvals a JOIN ai_invocations i ON i.id=a.invocation_id WHERE a.state='pending' AND a.expires_at<=NOW() AND i.state='awaiting_approval' AND i.approval_wait_id=a.id ORDER BY a.expires_at LIMIT 50 FOR UPDATE OF i,a SKIP LOCKED`)
		if err != nil {
			return err
		}
		type expired struct{ id, run, user, hook, runtime string }
		items := []expired{}
		for rows.Next() {
			var e expired
			if err := rows.Scan(&e.id, &e.run, &e.user, &e.hook, &e.runtime); err != nil {
				rows.Close()
				return err
			}
			items = append(items, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range items {
			if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='expired' WHERE id=$1`, e.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='running',updated_at=NOW() WHERE id=$1`, e.run); err != nil {
				return err
			}
			if err := queueAgentContinuationTx(ctx, tx, e.user, e.run, "approval.resume", e.id, AgentContinuation{RuntimeID: e.runtime, HookToken: e.hook, ApprovalID: e.id}); err != nil {
				return err
			}
		}
		return nil
	})
}
func sdkRunIdentity(id string) bool { return strings.HasPrefix(id, "invocation_") }
