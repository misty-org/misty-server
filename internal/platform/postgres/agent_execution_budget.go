package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrAgentExecutionTimeLimit = errors.New("agent_execution_time_limit")

type AgentExecutionBudget struct {
	Version     int        `json:"version"`
	LimitMS     int64      `json:"limit_ms"`
	ConsumedMS  int64      `json:"consumed_ms"`
	RemainingMS int64      `json:"remaining_ms"`
	Active      bool       `json:"active"`
	Deadline    *time.Time `json:"deadline,omitempty"`
}

// AgentRunExecutionBudget reads the persisted clock and, when begin is true,
// admits active execution. It never clears consumed time or changes run state.
func (db *Database) AgentRunExecutionBudget(ctx context.Context, userID, runID, runtimeID string, begin bool) (*AgentExecutionBudget, error) {
	if userID == "" || runID == "" || runtimeID == "" {
		return nil, ErrSpaceInvalid
	}
	var out *AgentExecutionBudget
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state, spaceID, pinnedRuntime, scope string
		var payload json.RawMessage
		query := `SELECT state,COALESCE(space_id,''),COALESCE(runtime_run_id,''),input,'ai.write' FROM space_runs WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`
		if strings.HasPrefix(runID, "invocation_") {
			query = `SELECT state,COALESCE(space_id,''),COALESCE(runtime_run_id,''),request_payload,CASE WHEN surface_id='sdk' THEN 'capabilities.invoke' ELSE 'ai.write' END FROM ai_invocations WHERE id=$1 AND user_id=$2 AND COALESCE(agent_run_id,'')='' FOR UPDATE`
		}
		if err := tx.QueryRowContext(ctx, query, runID, userID).Scan(&state, &spaceID, &pinnedRuntime, &payload, &scope); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceForbidden
			}
			return err
		}
		if pinnedRuntime != runtimeID || (begin && state != "running") {
			return ErrSpaceForbidden
		}
		authority, err := AppAuthorityFromPayload(payload)
		if err != nil {
			return err
		}
		if err = validateAppExecutionAuthorityTx(ctx, tx, authority, userID, spaceID, scope); err != nil {
			return err
		}
		if spaceID != "" {
			if _, err = requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		out, err = agentRunExecutionBudgetTx(ctx, tx, runID, begin)
		return err
	})
	return out, err
}

// Caller must hold the authoritative run row lock and validate its principal,
// runtime identity and state. Database time keeps competing workers on one clock.
func agentRunExecutionBudgetTx(ctx context.Context, tx *sql.Tx, runID string, begin bool) (*AgentExecutionBudget, error) {
	table := "space_runs"
	if strings.HasPrefix(runID, "invocation_") {
		table = "ai_invocations"
	}
	var out AgentExecutionBudget
	var stored int64
	var active sql.NullTime
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT execution_budget_version,execution_limit_ms,execution_consumed_ms,execution_active_at,clock_timestamp() FROM `+table+` WHERE id=$1`, runID).Scan(&out.Version, &out.LimitMS, &stored, &active, &now); err != nil {
		return nil, err
	}
	if out.Version == 0 {
		return &AgentExecutionBudget{}, nil
	}
	out.ConsumedMS = min(stored, out.LimitMS)
	if active.Valid {
		elapsed := now.Sub(active.Time)
		if elapsed > 0 {
			elapsedMS := elapsed.Milliseconds()
			if elapsed%time.Millisecond != 0 {
				elapsedMS++
			}
			out.ConsumedMS += min(elapsedMS, max(0, out.LimitMS-out.ConsumedMS))
		}
	}
	out.RemainingMS = max(0, out.LimitMS-out.ConsumedMS)
	if begin && out.RemainingMS == 0 {
		return nil, ErrAgentExecutionTimeLimit
	}
	if begin && !active.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET execution_active_at=$2 WHERE id=$1`, runID, now); err != nil {
			return nil, err
		}
		active = sql.NullTime{Time: now, Valid: true}
	}
	out.Active = active.Valid
	if active.Valid {
		deadline := active.Time.Add(time.Duration(max(0, out.LimitMS-stored)) * time.Millisecond)
		out.Deadline = &deadline
	}
	return &out, nil
}
