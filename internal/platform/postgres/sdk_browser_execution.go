package db

import (
	"context"
	"database/sql"
	"errors"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

// A configured browser target cannot widen an admitted run's device context.
// Space changes, context replacement and closed/revoked devices fail closed.
func (db *Database) ValidateSDKBrowserRunContext(ctx context.Context, userID, runID, spaceID string, b cap.BrowserBinding) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(
    SELECT 1 FROM agent_run_contexts c JOIN space_runs r ON r.id=c.run_id
     JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL
     WHERE c.owner_user_id=$1 AND c.run_id=$2 AND r.owner_user_id=$1 AND c.space_id=$3 AND r.space_id=$3
      AND c.opaque_ref=$4 AND c.device_id=$5 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? 'browser.inspect'
    UNION ALL
    SELECT 1 FROM ai_invocation_contexts c JOIN ai_invocations i ON i.id=c.invocation_id
     JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL
     WHERE c.user_id=$1 AND c.invocation_id=$2 AND i.user_id=$1 AND COALESCE(i.space_id,'')=$3
      AND c.opaque_ref=$4 AND c.device_id=$5 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? 'browser.inspect'
  )`, userID, runID, spaceID, b.ScopeID, b.DeviceID).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return ErrDeviceNotFound
		}
		return nil
	})
}

// Observing a changed review permanently retires that approval. Restoring the
// old page later cannot revive it; a new exact action must be reviewed.
func (db *Database) InvalidateSDKBrowserApproval(ctx context.Context, userID, effectID string) error {
	if !cap.ValidID(effectID) {
		return cap.ErrInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='expired' WHERE owner_user_id=$1 AND sdk_effect_id=$2 AND state IN ('pending','approved')`, userID, effectID)
		return err
	})
}

// Replay the original pause without re-inspecting or re-preparing a page while
// its runtime is suspended. This grants no execution or account authority.
func (db *Database) SDKPendingBrowserIntervention(ctx context.Context, userID, runID, runtimeID, callID string) (*AIInterventionWait, error) {
	var wait AIInterventionWait
	var count int
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT `+interventionColumns+`,COUNT(*) OVER() FROM ai_intervention_waits w WHERE w.user_id=$1 AND w.run_id=$2 AND ($3='' OR w.runtime_run_id=$3) AND ($4='' OR w.call_id=$4) AND w.state='pending' AND w.expires_at>NOW() AND EXISTS(SELECT 1 FROM `+interventionRunsSQL+` r WHERE r.id=w.run_id AND r.user_id=w.user_id AND r.runtime_run_id=w.runtime_run_id AND r.state='awaiting_intervention')`, userID, runID, runtimeID, callID).Scan(&wait.ID, &wait.RunID, &wait.ScopeID, &wait.DeviceID, &wait.Action, &wait.Reason, &wait.State, &wait.ExpiresAt, &wait.TargetLabel, &count)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrSpaceConflict
	}
	return &wait, nil
}
