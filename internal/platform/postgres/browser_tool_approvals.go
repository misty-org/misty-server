package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type BrowserApprovalTarget struct {
	ContextID string          `json:"contextId"`
	DeviceID  string          `json:"deviceId"`
	ScopeID   string          `json:"scopeId"`
	Label     string          `json:"label"`
	Snapshot  json.RawMessage `json:"-"`
	ExpiresAt time.Time       `json:"expiresAt"`
}

// BrowserApprovalTarget reads the latest observed page from this exact attached
// context. It never resolves a currently active tab or another account's view.
func (db *Database) BrowserToolApprovalTarget(ctx context.Context, userID, runID, runtimeID, scopeID, operation string) (*BrowserApprovalTarget, error) {
	target := &BrowserApprovalTarget{ScopeID: scopeID}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, _, err := deviceRunAuthorityTx(ctx, tx, userID, runID, &runtimeID, operation, true); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT c.id,c.device_id,c.display_name,c.expires_at,j.output FROM ai_invocation_contexts c
   JOIN ai_invocations i ON i.id=c.invocation_id AND i.space_id=c.space_id
   JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL
   JOIN LATERAL (SELECT output FROM workflow_device_node_jobs WHERE invocation_id=i.id AND ai_context_id=c.id AND operation='browser.inspect' AND state='completed' ORDER BY completed_at DESC,id DESC LIMIT 1) j ON true
   WHERE c.user_id=$1 AND c.invocation_id=$2 AND c.opaque_ref=$3 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? $4`, userID, runID, scopeID, operation).Scan(&target.ContextID, &target.DeviceID, &target.Label, &target.ExpiresAt, &target.Snapshot)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceInvalid
	}
	return target, err
}

// RequireBrowserToolApproval shares the durable approval table/outbox with SDK
// execution. The encrypted review is mandatory; it cannot be an opaque click.
func (db *Database) RequireBrowserToolApproval(ctx context.Context, userID, runID, runtimeID, callID, operation, hash, hook, summary string, target BrowserApprovalTarget, review ProtectedSDKApproval) (*AgentToolApproval, bool, error) {
	if (operation != "browser.click" && operation != "browser.interact") || callID == "" || len(callID) > 200 || hook == "" || len(hook) > 500 {
		return nil, false, ErrSpaceInvalid
	}
	result := &AgentToolApproval{}
	allowed := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, _, err := deviceRunAuthorityTx(ctx, tx, userID, runID, &runtimeID, operation, true); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocation_contexts c JOIN ai_invocations i ON i.id=c.invocation_id AND i.space_id=c.space_id AND i.user_id=c.user_id JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL WHERE c.user_id=$1 AND c.invocation_id=$2 AND c.id=$3 AND c.device_id=$4 AND c.opaque_ref=$5 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? $6)`, userID, runID, target.ContextID, target.DeviceID, target.ScopeID, operation).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrAppRuntimeForbidden
		}
		err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT `+sdkApprovalColumns+` FROM agent_run_tool_approvals WHERE invocation_id=$1 AND tool_call_id=$2`, runID, callID), result)
		if err == nil {
			if result.ToolName != operation || result.ArgumentsHash != hash || (result.State == "pending" && result.HookToken != hook) {
				return ErrAppRuntimeForbidden
			}
			allowed = result.State == "approved" && result.ExpiresAt.After(time.Now())
			return persistSDKApprovalReviewTx(ctx, tx, result, []ProtectedSDKApproval{review})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = scanAgentToolApproval(tx.QueryRowContext(ctx, `INSERT INTO agent_run_tool_approvals(id,invocation_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token,summary,expires_at)
   SELECT $1,id,$3,$4,$5,'dangerous',$6,$6,$7,$8,LEAST(expires_at,$9,NOW()+INTERVAL '24 hours') FROM ai_invocations WHERE id=$2 AND state='running' RETURNING `+sdkApprovalColumns, uuid.NewString(), runID, userID, callID, operation, hash, hook, summary, target.ExpiresAt), result)
		if err != nil {
			return err
		}
		if err := persistSDKApprovalReviewTx(ctx, tx, result, []ProtectedSDKApproval{review}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE ai_invocations SET state='awaiting_approval',approval_wait_id=$2,updated_at=NOW() WHERE id=$1`, runID, result.ID)
		return err
	})
	return result, allowed, err
}

// Internal execution lookup. Protected content is still unavailable to the model;
// the service uses it to compare a retry against its original exact review.
func (db *Database) BrowserToolApprovalByCall(ctx context.Context, userID, runID, callID string) (*AgentToolApproval, *ProtectedSDKApproval, error) {
	approval := &AgentToolApproval{}
	review := &ProtectedSDKApproval{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT `+sdkApprovalColumns+` FROM agent_run_tool_approvals WHERE invocation_id=$1 AND owner_user_id=$2 AND tool_call_id=$3 AND tool_name IN ('browser.click','browser.interact')`, runID, userID, callID), approval); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT sdk_effect_id::text,sdk_review_digest,sdk_review_ciphertext FROM agent_run_tool_approvals WHERE id=$1 AND sdk_review_ciphertext IS NOT NULL`, approval.ID).Scan(&review.EffectID, &review.Digest, &review.Ciphertext)
	})
	return approval, review, err
}
