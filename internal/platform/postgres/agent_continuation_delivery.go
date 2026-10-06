package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AgentContinuation struct {
	ObservedAfter *time.Time `json:"observed_after,omitempty"`
	RuntimeID     string     `json:"runtime_id"`
	HookToken     string     `json:"hook_token,omitempty"`
	WaitID        string     `json:"wait_id,omitempty"`
	Available     bool       `json:"available,omitempty"`
}

// invocationRunIdentity reports whether a run ID names an AI invocation rather
// than a Space run.
func invocationRunIdentity(id string) bool { return strings.HasPrefix(id, "invocation_") }

func queueAgentContinuationTx(ctx context.Context, tx *sql.Tx, userID, runID, operation, identity string, payload AgentContinuation) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_runtime_deliveries(id,user_id,run_id,operation,payload)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, operation+":"+runID+":"+identity, userID, runID, operation, encoded)
	return err
}

// QueueAgentDeviceResume atomically changes the wait and publishes its exact
// continuation. A later wait cannot be cleared by an acknowledgement of this one.
func (db *Database) QueueAgentDeviceResume(ctx context.Context, wait AgentDeviceWait) error {
	if invocationRunIdentity(wait.RunID) {
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
	if delivery.Operation != "device.resume" {
		return false, nil
	}
	var current bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if invocationRunIdentity(delivery.RunID) {
			return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations WHERE id=$1 AND user_id=$2 AND runtime_run_id=$3 AND device_wait_hook_token=$4 AND state IN ('running','awaiting_device') AND COALESCE(agent_run_id,'')='')`, delivery.RunID, delivery.UserID, payload.RuntimeID, payload.HookToken).Scan(&current)
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_runs r WHERE r.id=$1 AND r.owner_user_id=$2
   AND r.runtime_run_id=$3 AND r.state IN ('running','awaiting_device') AND r.device_wait_hook_token=$4)`,
			delivery.RunID, delivery.UserID, payload.RuntimeID, payload.HookToken).Scan(&current)
	})
	return current, err
}

func (db *Database) FinishAgentContinuation(ctx context.Context, delivery AgentRuntimeDelivery, payload AgentContinuation) error {
	if delivery.Operation != "device.resume" {
		return errors.New("unsupported continuation")
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if invocationRunIdentity(delivery.RunID) {
			_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET device_wait_hook_token='',device_wait_expires_at=NULL,device_wait_context_id='',device_wait_scope_id='',device_wait_capability='',device_wait_call_id='',device_wait_arguments_hash='',updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND device_wait_hook_token=$3 AND state<>'awaiting_device'`, delivery.RunID, payload.RuntimeID, payload.HookToken)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_runs SET device_wait_hook_token='',device_wait_expires_at=NULL,
    device_wait_scope_id='',device_wait_capability='',runtime_phase=CASE WHEN runtime_phase='device_resume_pending' THEN 'working' ELSE runtime_phase END,
    updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND device_wait_hook_token=$3`, delivery.RunID, payload.RuntimeID, payload.HookToken)
		return err
	})
}
