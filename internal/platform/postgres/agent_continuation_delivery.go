package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type AgentContinuation struct {
	ObservedAfter *time.Time `json:"observed_after,omitempty"`
	RuntimeID     string     `json:"runtime_id"`
	HookToken     string     `json:"hook_token,omitempty"`
	ApprovalID    string     `json:"approval_id,omitempty"`
	Available     bool       `json:"available,omitempty"`
	Approved      bool       `json:"approved,omitempty"`
}

func queueAgentContinuationTx(ctx context.Context, tx *sql.Tx, userID, runID, operation, identity string, payload AgentContinuation) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_runtime_deliveries(id,user_id,run_id,operation,payload)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, operation+":"+runID+":"+identity, userID, runID, operation, encoded)
	return err
}

func queueApprovalDeliveryTx(ctx context.Context, tx *sql.Tx, approval *AgentToolApproval) error {
	var runtimeID string
	if err := tx.QueryRowContext(ctx, `SELECT runtime_run_id FROM space_runs WHERE id=$1`, approval.RunID).Scan(&runtimeID); err != nil {
		return err
	}
	return queueAgentContinuationTx(ctx, tx, approval.OwnerUserID, approval.RunID, "approval.resume", approval.ID,
		AgentContinuation{RuntimeID: runtimeID, HookToken: approval.HookToken, ApprovalID: approval.ID, Approved: approval.State == "approved"})
}

// QueueAgentApprovalResume also adopts decided waits written by older servers.
func (db *Database) QueueAgentApprovalResume(ctx context.Context, approvalID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		approval := &AgentToolApproval{}
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT `+qualifiedAgentToolApprovalColumns("a")+`
   FROM agent_run_tool_approvals a JOIN space_runs r ON r.id=a.run_id
   WHERE a.id=$1 AND a.state IN ('approved','denied','expired') AND r.state IN ('running','awaiting_approval')
    AND (r.approval_wait_id=a.id OR r.approval_wait_id='') FOR UPDATE OF r,a`, approvalID), approval); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='running',approval_wait_id=$2,approval_state=$3,runtime_phase='approval_resume_pending',updated_at=NOW() WHERE id=$1`, approval.RunID, approval.ID, approval.State); err != nil {
			return err
		}
		return queueApprovalDeliveryTx(ctx, tx, approval)
	})
}

// QueueAgentDeviceResume atomically changes the wait and publishes its exact
// continuation. A later wait cannot be cleared by an acknowledgement of this one.
func (db *Database) QueueAgentDeviceResume(ctx context.Context, wait AgentDeviceWait) error {
	if sdkRunIdentity(wait.RunID) {
		return db.queueAIInvocationDeviceResume(ctx, wait)
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var userID, runtimeID string
		var expired bool
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,runtime_run_id,device_wait_expires_at<=NOW() FROM space_runs
   WHERE id=$1 AND state='awaiting_device' AND device_wait_hook_token=$2 FOR UPDATE`, wait.RunID, wait.HookToken).Scan(&userID, &runtimeID, &expired); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='running',runtime_phase='device_resume_pending',updated_at=NOW() WHERE id=$1`, wait.RunID); err != nil {
			return err
		}
		return queueAgentContinuationTx(ctx, tx, userID, wait.RunID, "device.resume", wait.HookToken,
			AgentContinuation{RuntimeID: runtimeID, HookToken: wait.HookToken, Available: wait.Available && !expired})
	})
}

func (db *Database) AgentContinuationCurrent(ctx context.Context, delivery AgentRuntimeDelivery, payload AgentContinuation) (bool, error) {
	if sdkRunIdentity(delivery.RunID) {
		if delivery.Operation == "device.resume" {
			var current bool
			err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
				return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations WHERE id=$1 AND user_id=$2 AND runtime_run_id=$3 AND device_wait_hook_token=$4 AND state IN ('running','awaiting_approval','awaiting_device') AND COALESCE(agent_run_id,'')='')`, delivery.RunID, delivery.UserID, payload.RuntimeID, payload.HookToken).Scan(&current)
			})
			return current, err
		}
		if delivery.Operation != "approval.resume" || payload.ApprovalID == "" {
			return false, nil
		}
		var current bool
		err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations i LEFT JOIN sdk_capability_invocations c ON c.invocation_id=i.id WHERE i.id=$1 AND i.user_id=$2 AND i.runtime_run_id=$3 AND i.approval_wait_id=$4 AND i.state='running' AND (c.invocation_id IS NULL OR c.cancel_requested_at IS NULL) AND COALESCE(i.agent_run_id,'')='')`, delivery.RunID, delivery.UserID, payload.RuntimeID, payload.ApprovalID).Scan(&current)
		})
		return current, err
	}

	var current bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_runs r WHERE r.id=$1 AND r.owner_user_id=$2
   AND r.runtime_run_id=$3 AND r.state IN ('running','awaiting_approval','awaiting_device')
   AND (($4='device.resume' AND r.device_wait_hook_token=$5) OR ($4='approval.resume' AND r.approval_wait_id=$6)))`,
			delivery.RunID, delivery.UserID, payload.RuntimeID, delivery.Operation, payload.HookToken, payload.ApprovalID).Scan(&current)
	})
	return current, err
}

func (db *Database) FinishAgentContinuation(ctx context.Context, delivery AgentRuntimeDelivery, payload AgentContinuation) error {
	if sdkRunIdentity(delivery.RunID) {
		if delivery.Operation == "device.resume" {
			return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET device_wait_hook_token='',device_wait_expires_at=NULL,device_wait_context_id='',device_wait_scope_id='',device_wait_capability='',device_wait_call_id='',device_wait_arguments_hash='',updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND device_wait_hook_token=$3 AND state<>'awaiting_device'`, delivery.RunID, payload.RuntimeID, payload.HookToken)
				return err
			})
		}
		if delivery.Operation != "approval.resume" || payload.ApprovalID == "" {
			return ErrSpaceInvalid
		}
		return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET approval_wait_id='',updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND approval_wait_id=$3`, delivery.RunID, payload.RuntimeID, payload.ApprovalID)
			return err
		})
	}

	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if delivery.Operation == "device.resume" {
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET device_wait_hook_token='',device_wait_expires_at=NULL,
    device_wait_scope_id='',device_wait_capability='',runtime_phase=CASE WHEN runtime_phase='device_resume_pending' THEN 'working' ELSE runtime_phase END,
    updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND device_wait_hook_token=$3`, delivery.RunID, payload.RuntimeID, payload.HookToken)
			return err
		}
		if delivery.Operation == "approval.resume" {
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET approval_wait_id='',runtime_phase=CASE WHEN runtime_phase='approval_resume_pending' THEN 'working' ELSE runtime_phase END,
    updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND approval_wait_id=$3`, delivery.RunID, payload.RuntimeID, payload.ApprovalID)
			return err
		}
		return errors.New("unsupported continuation")
	})
}
