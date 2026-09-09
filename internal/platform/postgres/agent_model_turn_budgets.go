package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var ErrAgentModelTurnLimit = errors.New("agent_model_turn_limit")

// ReserveAgentModelTurn runs before a durable model step starts. Stable node IDs
// make a lost admission response replayable; a new model turn consumes a new slot.
func (db *Database) ReserveAgentModelTurn(ctx context.Context, userID, runID, runtimeID, nodeID string) error {
	if userID == "" || runtimeID == "" || !strings.HasPrefix(nodeID, "model:") || len(nodeID) < 7 || len(nodeID) > 200 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state, spaceID, pinnedRuntime string
		var payload json.RawMessage
		var version, limit int
		query := `SELECT state,COALESCE(space_id,''),input,COALESCE(runtime_run_id,''),model_budget_version,model_turn_limit FROM space_runs WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`
		if strings.HasPrefix(runID, "invocation_") {
			query = `SELECT state,COALESCE(space_id,''),request_payload,COALESCE(runtime_run_id,''),model_budget_version,model_turn_limit FROM ai_invocations WHERE id=$1 AND user_id=$2 AND COALESCE(agent_run_id,'')='' FOR UPDATE`
		}
		if err := tx.QueryRowContext(ctx, query, runID, userID).Scan(&state, &spaceID, &payload, &pinnedRuntime, &version, &limit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceForbidden
			}
			return err
		}
		if pinnedRuntime != runtimeID || state != "running" {
			return ErrSpaceForbidden
		}
		authority, err := AppAuthorityFromPayload(payload)
		if err != nil {
			return err
		}
		if err := validateAppExecutionAuthorityTx(ctx, tx, authority, userID, spaceID, "ai.write"); err != nil {
			return err
		}
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		if _, err := agentRunExecutionBudgetTx(ctx, tx, runID, true); err != nil {
			return err
		}
		var routineMarker struct {
			Routine bool `json:"_misty_routine_execution"`
		}
		if json.Unmarshal(payload, &routineMarker) != nil {
			return ErrSpaceInvalid
		}
		if routineMarker.Routine {
			if err := routineAgentModelTurnTx(ctx, tx, userID, runID, runtimeID, nodeID); err != nil {
				return err
			}
		}
		if version == 0 {
			return nil
		}
		var previousRuntime string
		err = tx.QueryRowContext(ctx, `SELECT runtime_run_id FROM agent_model_turn_claims WHERE run_id=$1 AND user_id=$2 AND node_id=$3`, runID, userID, nodeID).Scan(&previousRuntime)
		if err == nil {
			if previousRuntime != runtimeID {
				return ErrSpaceForbidden
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var consumed int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_model_turn_claims WHERE run_id=$1`, runID).Scan(&consumed); err != nil {
			return err
		}
		if consumed >= limit {
			return ErrAgentModelTurnLimit
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_model_turn_claims(run_id,user_id,runtime_run_id,node_id) VALUES($1,$2,$3,$4)`, runID, userID, runtimeID, nodeID)
		return err
	})
}
