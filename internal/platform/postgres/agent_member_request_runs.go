package db

import (
	"context"
	"database/sql"
	"errors"
)

func queueMemberRequestRunTx(ctx context.Context, tx *sql.Tx, runID, spaceID, agentID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_run_jobs(run_id,space_id,agent_id,trigger_kind) VALUES($1,$2,$3,'delegated')`, runID, spaceID, agentID)
	return err
}

// memberRequestValidTx reports whether both members still share an active
// Space and the target agent is still in it, and still public to the asker.
func memberRequestValidTx(ctx context.Context, tx *sql.Tx, runID string) (bool, error) {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_member_requests q
		JOIN space_agents m ON m.space_id=q.space_id AND m.agent_id=q.target_agent_id AND m.owner_user_id=q.target_owner_user_id
			AND (m.visibility='public' OR m.owner_user_id=q.requester_user_id)
		JOIN space_members requester ON requester.space_id=q.space_id AND requester.user_id=q.requester_user_id
		JOIN spaces s ON s.id=q.space_id AND s.lifecycle_state='active'
		WHERE q.child_run_id=$1)`, runID).Scan(&valid)
	return valid, err
}

// ValidateAgentMemberRequestRun re-checks, before each tool call, that both
// members still share the Space and the target agent is still in it.
func (db *Database) ValidateAgentMemberRequestRun(ctx context.Context, runID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		valid, err := memberRequestValidTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		if !valid {
			return ErrAgentNotInSpace
		}
		return nil
	})
}

// CancelAgentMemberRequest stops a request its sender no longer wants. Work
// that already finished keeps its result.
func (db *Database) CancelAgentMemberRequest(ctx context.Context, requesterUserID, requestID string) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var runID, owner, runtime, state string
		err := tx.QueryRowContext(ctx, `SELECT r.id,r.owner_user_id,COALESCE(r.runtime_run_id,''),r.state FROM agent_member_requests q
			JOIN space_runs r ON r.id=q.child_run_id WHERE q.id=$1 AND q.requester_user_id=$2 FOR UPDATE OF r`, requestID, requesterUserID).Scan(&runID, &owner, &runtime, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		switch state {
		case "completed", "completed_with_errors", "failed", "canceled", "rejected":
			return ErrSpaceConflict
		}
		if err := cancelMistyRunTx(ctx, tx, owner, runID, runtime); err != nil {
			return err
		}
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1`, requestID), out)
	})
	return out, err
}
