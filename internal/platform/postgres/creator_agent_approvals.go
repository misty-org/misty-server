package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AgentToolApproval struct {
	ID            string     `json:"id"`
	RunID         string     `json:"run_id"`
	OwnerUserID   string     `json:"owner_user_id"`
	ToolCallID    string     `json:"tool_call_id"`
	ToolName      string     `json:"tool_name"`
	Impact        string     `json:"impact"`
	ArgumentsHash string     `json:"arguments_hash"`
	SignedCall    string     `json:"signed_call"`
	HookToken     string     `json:"-"`
	Summary       string     `json:"summary"`
	State         string     `json:"state"`
	ExpiresAt     time.Time  `json:"expires_at"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
}

const agentToolApprovalColumns = `id,run_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token,summary,state,expires_at,decided_at`

func scanAgentToolApproval(row scanner, out *AgentToolApproval) error {
	return row.Scan(&out.ID, &out.RunID, &out.OwnerUserID, &out.ToolCallID, &out.ToolName, &out.Impact, &out.ArgumentsHash, &out.SignedCall, &out.HookToken, &out.Summary, &out.State, &out.ExpiresAt, &out.DecidedAt)
}

func (db *Database) RequireCreatorToolApproval(ctx context.Context, run *SpaceRun, callID, toolName, impact, argumentsHash, signedCall, hookToken, summary string) (*AgentToolApproval, bool, error) {
	if run == nil || strings.TrimSpace(callID) == "" || strings.TrimSpace(hookToken) == "" {
		return nil, false, ErrSpaceInvalid
	}
	out := &AgentToolApproval{}
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, run.OwnerUserID, run.SpaceID, run.AgentID); err != nil {
			return err
		}
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `SELECT `+agentToolApprovalColumns+` FROM agent_run_tool_approvals WHERE run_id=$1 AND tool_call_id=$2`, run.ID, callID), out); err == nil {
			if out.ArgumentsHash != argumentsHash || out.ToolName != toolName || out.SignedCall != signedCall {
				return ErrSpaceForbidden
			}
			if out.State == "approved" {
				allowed = true
				return nil
			}
			if out.State == "denied" || out.State == "expired" {
				return nil
			}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out.ID = "approval_" + uuid.NewString()
		out.RunID = run.ID
		out.OwnerUserID = run.OwnerUserID
		out.ToolCallID = callID
		out.ToolName = toolName
		out.Impact = impact
		out.ArgumentsHash = argumentsHash
		out.SignedCall = signedCall
		out.HookToken = hookToken
		out.Summary = summary
		out.State = "pending"
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `INSERT INTO agent_run_tool_approvals(id,run_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token,summary)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+agentToolApprovalColumns, out.ID, out.RunID, out.OwnerUserID, out.ToolCallID, out.ToolName, out.Impact, out.ArgumentsHash, out.SignedCall, out.HookToken, out.Summary), out); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_approval',approval_state='pending',runtime_phase='awaiting_approval',updated_at=NOW() WHERE id=$1 AND state='running'`, run.ID)
		return err
	})
	return out, allowed, err
}

func (db *Database) DecideCreatorToolApproval(ctx context.Context, ownerUserID, runID, approvalID string, approve bool) (*AgentToolApproval, error) {
	out := &AgentToolApproval{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='expired' WHERE id=$1 AND state='pending' AND expires_at<=NOW()`, approvalID); err != nil {
			return err
		}
		state := "denied"
		if approve {
			state = "approved"
		}
		if err := scanAgentToolApproval(tx.QueryRowContext(ctx, `UPDATE agent_run_tool_approvals a SET state=$1,decided_by_user_id=$2,decided_at=NOW()
			FROM space_runs r WHERE a.id=$3 AND a.run_id=$4 AND a.run_id=r.id AND a.owner_user_id=$2 AND r.owner_user_id=$2 AND a.state='pending' AND a.expires_at>NOW()
			RETURNING `+qualifiedAgentToolApprovalColumns("a"), state, ownerUserID, approvalID, runID), out); err != nil {
			return err
		}
		approvalState := "denied"
		if approve {
			approvalState = "approved"
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='running',
			effective_run_mode=CASE WHEN $1 THEN 'full' ELSE effective_run_mode END,
			approval_state=$2,runtime_phase='approval_resume_pending',updated_at=NOW()
			WHERE id=$3 AND owner_user_id=$4 AND state='awaiting_approval'`, approve, approvalState, runID, ownerUserID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return out, err
}

// CreatorToolApprovalResumesPending returns decided approvals whose workflow
// hook has not yet been durably acknowledged. Keeping this state queryable
// closes the gap between the database decision and a runtime restart.
func (db *Database) CreatorToolApprovalResumesPending(ctx context.Context, limit int) ([]AgentToolApproval, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AgentToolApproval{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+qualifiedAgentToolApprovalColumns("a")+` FROM agent_run_tool_approvals a
			JOIN space_runs r ON r.id=a.run_id
			WHERE a.state IN ('approved','denied') AND r.state='running' AND r.runtime_phase='approval_resume_pending'
			ORDER BY a.decided_at,a.id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentToolApproval
			if err := scanAgentToolApproval(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) MarkCreatorToolApprovalResumed(ctx context.Context, runID, approvalID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_runs r SET runtime_phase='working',runtime_heartbeat_at=NOW(),updated_at=NOW()
			FROM agent_run_tool_approvals a WHERE r.id=$1 AND a.id=$2 AND a.run_id=r.id
			  AND a.state IN ('approved','denied') AND r.state='running' AND r.runtime_phase='approval_resume_pending'`, runID, approvalID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed > 0 {
			return err
		}
		// The workflow may advance immediately after accepting the hook. Treat an
		// already-advanced or terminal run as an idempotent acknowledgement rather
		// than turning that harmless race into a failed approval request.
		var state, phase, approvalState string
		err = tx.QueryRowContext(ctx, `SELECT r.state,r.runtime_phase,a.state FROM space_runs r
			JOIN agent_run_tool_approvals a ON a.run_id=r.id
			WHERE r.id=$1 AND a.id=$2`, runID, approvalID).Scan(&state, &phase, &approvalState)
		if err == nil && (approvalState == "approved" || approvalState == "denied") &&
			(state != "running" || phase != "approval_resume_pending") {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return ErrSpaceConflict
	})
}

func qualifiedAgentToolApprovalColumns(alias string) string {
	parts := strings.Split(agentToolApprovalColumns, ",")
	for i := range parts {
		parts[i] = alias + "." + parts[i]
	}
	return strings.Join(parts, ",")
}

// ExpireCreatorToolApprovals returns expired hooks whose durable workflows still
// need to be resumed with a denial. They remain discoverable until the runtime
// acknowledges the resume, making reconciliation idempotent across restarts.
func (db *Database) ExpireCreatorToolApprovals(ctx context.Context, limit int) ([]AgentToolApproval, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AgentToolApproval{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='expired' WHERE state='pending' AND expires_at<=NOW()`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+qualifiedAgentToolApprovalColumns("a")+` FROM agent_run_tool_approvals a
			JOIN space_runs r ON r.id=a.run_id WHERE a.state='expired' AND r.state='awaiting_approval'
			ORDER BY a.expires_at,a.id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentToolApproval
			if err := scanAgentToolApproval(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) MarkExpiredCreatorToolApprovalResumed(ctx context.Context, runID, approvalID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_runs r SET state='running',approval_state='expired',runtime_phase='working',runtime_heartbeat_at=NOW(),updated_at=NOW()
			FROM agent_run_tool_approvals a WHERE r.id=$1 AND a.id=$2 AND a.run_id=r.id AND a.state='expired' AND r.state='awaiting_approval'`, runID, approvalID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed > 0 {
			return err
		}
		var state, phase, approvalState string
		err = tx.QueryRowContext(ctx, `SELECT r.state,r.runtime_phase,a.state FROM space_runs r
			JOIN agent_run_tool_approvals a ON a.run_id=r.id
			WHERE r.id=$1 AND a.id=$2`, runID, approvalID).Scan(&state, &phase, &approvalState)
		if err == nil && approvalState == "expired" &&
			(state != "awaiting_approval" || phase != "awaiting_approval") {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return ErrSpaceConflict
	})
}
