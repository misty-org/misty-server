package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type AIInterventionWait struct {
	TargetLabel string    `json:"targetLabel"`
	ID          string    `json:"id"`
	RunID       string    `json:"runId"`
	ScopeID     string    `json:"scopeId"`
	DeviceID    string    `json:"deviceId"`
	Action      string    `json:"action"`
	Reason      string    `json:"reason"`
	State       string    `json:"state"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

const interventionColumns = `id,run_id,scope_id,device_id,action,reason,state,expires_at,target_label`

func scanIntervention(row interface{ Scan(...any) error }, w *AIInterventionWait) error {
	return row.Scan(&w.ID, &w.RunID, &w.ScopeID, &w.DeviceID, &w.Action, &w.Reason, &w.State, &w.ExpiresAt, &w.TargetLabel)
}

// This capability pauses before a browser action. It never retries a send or
// treats a user's readiness confirmation as proof of delivery/account identity.
// AwaitAIUserIntervention preserves the existing public quick-AI interface.
func (db *Database) AwaitAIUserIntervention(ctx context.Context, user, run, runtime, call, hook, scope, action, reason, digest string) (*AIInterventionWait, error) {
	return db.AwaitAgentUserIntervention(ctx, user, run, runtime, call, hook, scope, action, reason, digest)
}
func (db *Database) AwaitAgentUserIntervention(ctx context.Context, user, run, runtime, call, hook, scope, action, reason, digest string) (*AIInterventionWait, error) {
	if call == "" || len(call) > 200 || hook == "" || len(hook) > 500 || len(scope) < 8 || len(scope) > 256 || len(reason) < 1 || len(reason) > 1000 || len(digest) != 64 {
		return nil, ErrSpaceInvalid
	}
	switch action {
	case "sign_in", "account_confirmation", "challenge", "open_target", "review":
	default:
		return nil, ErrSpaceInvalid
	}
	out := &AIInterventionWait{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := agentInterventionAuthorityTx(ctx, tx, user, run, runtime); err != nil {
			return err
		}
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM `+interventionRunsSQL+` r WHERE id=$1 AND user_id=$2`, run, user).Scan(&state); err != nil {
			return err
		}
		var hash, oldHook string
		err := tx.QueryRowContext(ctx, `SELECT `+interventionColumns+`,arguments_hash,hook_token FROM ai_intervention_waits WHERE run_id=$1 AND call_id=$2 AND user_id=$3 AND runtime_run_id=$4`, run, call, user, runtime).Scan(&out.ID, &out.RunID, &out.ScopeID, &out.DeviceID, &out.Action, &out.Reason, &out.State, &out.ExpiresAt, &out.TargetLabel, &hash, &oldHook)
		if err == nil {
			if state == "awaiting_intervention" && out.State != "pending" {
				return ErrSpaceConflict
			}
			if hash != digest || out.State == "pending" && oldHook != hook {
				return ErrSpaceConflict
			}
			if out.State == "ready" {
				ok, err := aiInterventionTargetValidTx(ctx, tx, out.ID)
				if err != nil {
					return err
				}
				if !ok {
					return ErrSpaceForbidden
				}
			}
			if out.State != "pending" {
				if _, err := tx.ExecContext(ctx, `UPDATE ai_intervention_waits SET consumed_at=COALESCE(consumed_at,NOW()) WHERE id=$1`, out.ID); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state != "running" {
			return ErrSpaceConflict
		}
		var contextID, device, label string
		var expiry time.Time
		var count int
		err = tx.QueryRowContext(ctx, `SELECT c.id,c.device_id,c.display_name,LEAST(c.expires_at,r.expires_at,NOW()+INTERVAL '24 hours'),COUNT(*) OVER() FROM `+interventionContextsSQL+` c JOIN `+interventionRunsSQL+` r ON r.id=c.run_id AND r.user_id=c.user_id AND r.space_id=c.space_id JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=c.user_id AND d.revoked_at IS NULL WHERE c.run_id=$1 AND c.user_id=$2 AND c.opaque_ref=$3 AND c.kind='browser_tab' AND c.state='attached' AND c.capabilities ? 'browser.inspect' AND c.expires_at>NOW() AND r.expires_at>NOW()`, run, user, scope).Scan(&contextID, &device, &label, &expiry, &count)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrSpaceConflict
		}
		*out = AIInterventionWait{TargetLabel: label, ID: uuid.NewString(), RunID: run, ScopeID: scope, DeviceID: device, Action: action, Reason: reason, State: "pending", ExpiresAt: expiry}
		var invocationID, spaceRunID any
		if sdkRunIdentity(run) {
			invocationID = run
		} else {
			spaceRunID = run
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_intervention_waits(id,user_id,invocation_id,space_run_id,runtime_run_id,call_id,arguments_hash,hook_token,context_id,device_id,scope_id,action,reason,expires_at,target_label) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, out.ID, user, invocationID, spaceRunID, runtime, call, digest, hook, contextID, device, scope, action, reason, expiry, label)
		if err != nil {
			return err
		}
		return agentInterventionStateTx(ctx, tx, user, run, out.ID, "awaiting_intervention", "awaiting_intervention", "User action is required in the attached browser. Review the pending request in Misty.")
	})
	return out, err
}
func aiInterventionTargetValidTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_intervention_waits w JOIN `+interventionRunsSQL+` r ON r.id=w.run_id AND r.user_id=w.user_id AND r.runtime_run_id=w.runtime_run_id JOIN `+interventionContextsSQL+` c ON c.id=w.context_id AND c.run_id=r.id AND c.user_id=r.user_id AND c.space_id=r.space_id AND c.device_id=w.device_id AND c.opaque_ref=w.scope_id JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=c.user_id WHERE w.id=$1 AND c.kind='browser_tab' AND c.state='attached' AND c.capabilities ? 'browser.inspect' AND c.expires_at>NOW() AND d.revoked_at IS NULL AND r.expires_at>NOW())`, id).Scan(&valid)
	return valid, err
}
func (db *Database) AIUserInterventions(ctx context.Context, user string) ([]AIInterventionWait, error) {
	if AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	out := []AIInterventionWait{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT w.id,w.run_id,w.scope_id,w.device_id,w.action,w.reason,w.state,w.expires_at,w.target_label FROM ai_intervention_waits w JOIN `+interventionRunsSQL+` r ON r.id=w.run_id AND r.user_id=w.user_id WHERE w.user_id=$1 AND w.state='pending' AND r.state='awaiting_intervention' ORDER BY w.created_at LIMIT 100`, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIInterventionWait
			if err := scanIntervention(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
func (db *Database) DecideAIUserIntervention(ctx context.Context, user, id string, ready bool) error {
	if AppAuthorityFromContext(ctx) != nil {
		return ErrAppRuntimeForbidden
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var run, runtime, hook, state string
		var expired bool
		// Parent first: same lock order as admission, cancellation, and callbacks.
		if err := tx.QueryRowContext(ctx, `SELECT run_id FROM ai_intervention_waits WHERE id=$1 AND user_id=$2`, id, user).Scan(&run); err != nil {
			return ErrSpaceConflict
		}
		var err error
		runtime, err = lockAgentInterventionParentTx(ctx, tx, user, run)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT hook_token,state,expires_at<=NOW() FROM ai_intervention_waits WHERE id=$1 AND runtime_run_id=$2 FOR UPDATE`, id, runtime).Scan(&hook, &state, &expired); err != nil {
			return err
		}
		if state != "pending" {
			return ErrSpaceConflict
		}
		allowed, err := aiInterventionTargetValidTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := agentInterventionAuthorityTx(ctx, tx, user, run, runtime); err != nil {
			if !deviceAuthorityDenied(err) {
				return err
			}
			allowed = false
		}
		state = "declined"
		if ready && allowed && !expired {
			state = "ready"
		}
		if expired {
			state = "expired"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_intervention_waits SET state=$2 WHERE id=$1`, id, state); err != nil {
			return err
		}
		if err := agentInterventionStateTx(ctx, tx, user, run, id, "running", "intervention_resume_pending", "The user-action wait ended. Rechecking the original target before continuing."); err != nil {
			return err
		}
		return queueAgentContinuationTx(ctx, tx, user, run, "intervention.resume", id, AgentContinuation{RuntimeID: runtime, HookToken: hook, ApprovalID: id, Available: state == "ready"})
	})
}
func (db *Database) ExpireAIUserInterventions(ctx context.Context) error {
	type due struct{ user, id string }
	items := []due{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT w.user_id,w.id FROM ai_intervention_waits w JOIN `+interventionRunsSQL+` r ON r.id=w.run_id AND r.user_id=w.user_id WHERE w.state='pending' AND w.expires_at<=NOW() AND r.state='awaiting_intervention' ORDER BY w.expires_at LIMIT 20`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.user, &d.id); err != nil {
				return err
			}
			items = append(items, d)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := db.DecideAIUserIntervention(ctx, item.user, item.id, false); err != nil && !errors.Is(err, ErrSpaceConflict) {
			return err
		}
	}
	return nil
}
func (db *Database) AIUserInterventionContinuation(ctx context.Context, delivery AgentRuntimeDelivery, p AgentContinuation) (bool, bool, error) {
	current, allowed := false, false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_intervention_waits w JOIN `+interventionRunsSQL+` r ON r.id=w.run_id AND r.user_id=w.user_id WHERE w.id=$1 AND w.run_id=$2 AND w.user_id=$3 AND w.runtime_run_id=$4 AND r.runtime_run_id=w.runtime_run_id AND w.hook_token=$5 AND w.state IN ('ready','declined','expired') AND w.consumed_at IS NULL AND r.state='running')`, p.ApprovalID, delivery.RunID, delivery.UserID, p.RuntimeID, p.HookToken).Scan(&current); err != nil || !current {
			return err
		}
		if !p.Available {
			return nil
		}
		if err := agentInterventionAuthorityTx(ctx, tx, delivery.UserID, delivery.RunID, p.RuntimeID); err != nil {
			if deviceAuthorityDenied(err) {
				return nil
			}
			return err
		}
		var err error
		allowed, err = aiInterventionTargetValidTx(ctx, tx, p.ApprovalID)
		return err
	})
	return current, allowed, err
}
