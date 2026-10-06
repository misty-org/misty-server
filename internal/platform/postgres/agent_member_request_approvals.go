package db

import (
	"context"
	"database/sql"
	"errors"
)

func queueMemberRequestRunTx(ctx context.Context, tx *sql.Tx, runID, spaceID, agentID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_run_jobs(run_id,space_id,task_id,agent_id,trigger_kind) VALUES($1,$2,NULL,$3,'delegated')`, runID, spaceID, agentID)
	return err
}

// memberRequestValidTx reports whether both members still share an active
// Space and the target agent's listing still accepts requests.
func memberRequestValidTx(ctx context.Context, tx *sql.Tx, runID string) (bool, error) {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_member_requests q
		JOIN space_agent_listings l ON l.space_id=q.space_id AND l.agent_id=q.target_agent_id AND l.owner_user_id=q.target_owner_user_id AND l.accept_policy<>'off'
		JOIN space_members owner_member ON owner_member.space_id=q.space_id AND owner_member.user_id=q.target_owner_user_id
		JOIN space_members requester ON requester.space_id=q.space_id AND requester.user_id=q.requester_user_id
		JOIN spaces s ON s.id=q.space_id AND s.lifecycle_state='active'
		WHERE q.child_run_id=$1)`, runID).Scan(&valid)
	return valid, err
}

// ValidateAgentMemberRequestRun re-checks, before each tool call, that both
// members still share the Space and the target agent still accepts requests.
func (db *Database) ValidateAgentMemberRequestRun(ctx context.Context, runID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		valid, err := memberRequestValidTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		if !valid {
			return ErrAgentListingUnavailable
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
		if _, err := tx.ExecContext(ctx, `UPDATE agent_member_requests SET approval='declined',decided_at=NOW() WHERE id=$1 AND approval='pending'`, requestID); err != nil {
			return err
		}
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1`, requestID), out)
	})
	return out, err
}

// PendingAgentMemberRequests lists requests waiting for the caller to approve
// work for one of their agents.
func (db *Database) PendingAgentMemberRequests(ctx context.Context, ownerUserID string) ([]AgentMemberRequest, error) {
	items := []AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, agentMemberRequestSelect+`WHERE q.target_owner_user_id=$1 AND q.approval='pending' AND r.state='queued'
			AND q.created_at>=NOW()-`+memberRequestApprovalTTL+` ORDER BY q.created_at LIMIT 100`, ownerUserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentMemberRequest
			if err := scanAgentMemberRequest(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// DecideAgentMemberRequest lets the target agent's owner approve or decline a
// waiting request. Approval queues the work, which the owner then
// pays for; declining ends it without running anything.
func (db *Database) DecideAgentMemberRequest(ctx context.Context, ownerUserID, requestID string, approve bool) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var runID, spaceID, agentID, state, approval string
		var expired bool
		err := tx.QueryRowContext(ctx, `SELECT q.child_run_id,q.space_id,q.target_agent_id,r.state,q.approval,q.created_at<NOW()-`+memberRequestApprovalTTL+`
			FROM agent_member_requests q JOIN space_runs r ON r.id=q.child_run_id
			WHERE q.id=$1 AND q.target_owner_user_id=$2 FOR UPDATE OF q,r`, requestID, ownerUserID).Scan(&runID, &spaceID, &agentID, &state, &approval, &expired)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if approval != "pending" || state != "queued" {
			return ErrSpaceConflict
		}
		event, decision := "agent.request.declined", "declined"
		switch {
		case expired:
			event, decision = "agent.request.expired", "expired"
			_, err = tx.ExecContext(ctx, `UPDATE space_runs SET state='rejected',error_code='approval_expired',
				error_message='The request expired before its owner approved it.',completed_at=NOW(),updated_at=NOW() WHERE id=$1`, runID)
		case approve:
			valid, validErr := memberRequestValidTx(ctx, tx, runID)
			if validErr != nil {
				return validErr
			}
			if !valid {
				return ErrAgentListingUnavailable
			}
			event, decision = "agent.request.approved", "approved"
			if _, err = tx.ExecContext(ctx, `UPDATE space_runs SET state='queued',updated_at=NOW() WHERE id=$1`, runID); err == nil {
				err = queueMemberRequestRunTx(ctx, tx, runID, spaceID, agentID)
			}
		default:
			_, err = tx.ExecContext(ctx, `UPDATE space_runs SET state='rejected',error_code='request_declined',
				error_message='The agent''s owner declined this request.',completed_at=NOW(),updated_at=NOW() WHERE id=$1`, runID)
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_member_requests SET approval=$2,decided_at=NOW() WHERE id=$1`, requestID, decision); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, ownerUserID, event, requestID, map[string]any{"request_id": requestID, "target_agent_id": agentID}); err != nil {
			return err
		}
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1`, requestID), out)
	})
	return out, err
}
