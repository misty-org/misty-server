package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (db *Database) CancelSpaceRun(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, requester string
		if err := tx.QueryRowContext(ctx, `SELECT space_id,requesting_member_id FROM space_runs WHERE id=$1`, runID).Scan(&spaceID, &requester); err != nil {
			return err
		}
		if requester != userID {
			return ErrSpaceForbidden
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAskRun); err != nil {
			return err
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW() WHERE id=$1 AND state IN ('queued','running','awaiting_approval','cooldown') RETURNING `+spaceRunColumns, runID), out); err != nil {
			return err
		}
		_, _ = tx.ExecContext(ctx, `UPDATE space_run_approvals SET state='canceled',decided_by_user_id=$1,decided_at=NOW() WHERE run_id=$2 AND state='pending'`, userID, runID)
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.run.canceled", runID, map[string]any{})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func requireRunResourceEnabledTx(ctx context.Context, tx *sql.Tx, run *SpaceRun) error {
	var enabled bool
	var err error
	switch run.ResourceKind {
	case "agent":
		err = tx.QueryRowContext(ctx, `SELECT a.enabled AND a.deleted_at IS NULL AND EXISTS(
			SELECT 1 FROM space_members m WHERE m.space_id=$2 AND m.user_id=a.owner_user_id)
			FROM misty_ask_identities a WHERE a.id=$1`, run.ResourceID, run.SpaceID).Scan(&enabled)
	default:
		return ErrSpaceInvalid
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSpaceNotFound
	}
	if err != nil {
		return err
	}
	if !enabled {
		return ErrSpaceInvalid
	}
	return nil
}

func (db *Database) RunApprovals(ctx context.Context, userID, runID string) ([]RunApproval, error) {
	if _, err := db.SpaceRun(ctx, userID, runID); err != nil {
		return nil, err
	}
	items := []RunApproval{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,run_id,requested_from_user_id,COALESCE(decided_by_user_id,''),action_summary,proposed_actions,state,created_at,decided_at,expires_at FROM space_run_approvals WHERE run_id=$1 AND requested_from_user_id=$2 ORDER BY created_at`, runID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RunApproval
			if err := rows.Scan(&item.ID, &item.RunID, &item.RequestedFromUserID, &item.DecidedByUserID, &item.ActionSummary, &item.ProposedActions, &item.State, &item.CreatedAt, &item.DecidedAt, &item.ExpiresAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) RunActions(ctx context.Context, userID, runID string) ([]RunAction, error) {
	if _, err := db.SpaceRun(ctx, userID, runID); err != nil {
		return nil, err
	}
	items := []RunAction{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,run_id,action_kind,summary,details,destructive,state,performed_at,created_at FROM space_run_actions WHERE run_id=$1 ORDER BY created_at`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RunAction
			if err := rows.Scan(&item.ID, &item.RunID, &item.ActionKind, &item.Summary, &item.Details, &item.Destructive, &item.State, &item.PerformedAt, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) RecordRunAction(ctx context.Context, runID, kind, summary string, details json.RawMessage, destructive bool, state string) error {
	if !validJSONObject(details) {
		details = json.RawMessage(`{}`)
	}
	if state == "" {
		state = "completed"
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var performed any
		if state == "completed" || state == "failed" {
			performed = time.Now().UTC()
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_run_actions(id,run_id,action_kind,summary,details,destructive,state,performed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, "runaction_"+uuid.NewString(), runID, kind, summary, details, destructive, state, performed)
		return err
	})
}

// ClaimRunResponsePublication serializes completion delivery for a run. The
// claim is retryable after a failed delivery and prevents device completion
// replays from posting duplicate conversation responses.
func (db *Database) ClaimRunResponsePublication(ctx context.Context, runID string) (string, bool, error) {
	actionID := ""
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "run-response:"+runID); err != nil {
			return err
		}
		var state string
		err := tx.QueryRowContext(ctx, `SELECT id,state FROM space_run_actions WHERE run_id=$1 AND action_kind='conversation_response' ORDER BY created_at DESC LIMIT 1`, runID).Scan(&actionID, &state)
		if err == nil {
			if state == "completed" || state == "approved" {
				return nil
			}
			_, err = tx.ExecContext(ctx, `UPDATE space_run_actions SET state='approved',details='{}'::jsonb,performed_at=NULL WHERE id=$1`, actionID)
			claimed = err == nil
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var terminal bool
		if err := tx.QueryRowContext(ctx, `SELECT state IN ('completed','failed','canceled') FROM space_runs WHERE id=$1`, runID).Scan(&terminal); err != nil {
			return err
		}
		if !terminal {
			return nil
		}
		actionID = "runaction_" + uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO space_run_actions(id,run_id,action_kind,summary,details,destructive,state) VALUES($1,$2,'conversation_response','Deliver terminal result to the source conversation','{}'::jsonb,FALSE,'approved')`, actionID, runID)
		claimed = err == nil
		return err
	})
	return actionID, claimed, err
}

func (db *Database) FinishRunResponsePublication(ctx context.Context, actionID, state string, details json.RawMessage) error {
	if actionID == "" || state != "completed" && state != "failed" || !validJSONObject(details) {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_run_actions SET state=$1,details=$2,performed_at=NOW() WHERE id=$3 AND action_kind='conversation_response' AND state='approved'`, state, details, actionID)
		return err
	})
}
