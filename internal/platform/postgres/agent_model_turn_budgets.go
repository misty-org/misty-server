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
		var version, limit int
		query := `SELECT state,COALESCE(space_id,''),COALESCE(runtime_run_id,''),model_budget_version,model_turn_limit FROM space_runs WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`
		if strings.HasPrefix(runID, "invocation_") {
			query = `SELECT state,COALESCE(space_id,''),COALESCE(runtime_run_id,''),model_budget_version,model_turn_limit FROM ai_invocations WHERE id=$1 AND user_id=$2 AND COALESCE(agent_run_id,'')='' FOR UPDATE`
		}
		if err := tx.QueryRowContext(ctx, query, runID, userID).Scan(&state, &spaceID, &pinnedRuntime, &version, &limit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceForbidden
			}
			return err
		}
		if pinnedRuntime != runtimeID || state != "running" {
			return ErrSpaceForbidden
		}
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		if _, err := agentRunExecutionBudgetTx(ctx, tx, runID, true); err != nil {
			return err
		}
		if version == 0 {
			return nil
		}
		var previousRuntime string
		err := tx.QueryRowContext(ctx, `SELECT runtime_run_id FROM agent_model_turn_claims WHERE run_id=$1 AND user_id=$2 AND node_id=$3`, runID, userID, nodeID).Scan(&previousRuntime)
		if err == nil {
			if previousRuntime != runtimeID {
				return ErrSpaceForbidden
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Screen planning calls (one per action of a browser_act goal) and
		// context compaction summaries are metered as usage but do not spend
		// the run's agent turns.
		if !TurnFreeModelNode(nodeID) {
			var consumed int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_model_turn_claims WHERE run_id=$1 AND node_id NOT LIKE 'model:screen:%' AND node_id NOT LIKE 'model:compact:%'`, runID).Scan(&consumed); err != nil {
				return err
			}
			if consumed >= limit {
				return ErrAgentModelTurnLimit
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_model_turn_claims(run_id,user_id,runtime_run_id,node_id) VALUES($1,$2,$3,$4)`, runID, userID, runtimeID, nodeID)
		return err
	})
}

// Native personal agents, including their separate team browser windows, need
// multiple inspection/action turns per UI step.
// Pin this allowance at admission; retries never rewrite an existing budget.
func invocationModelTurnLimitTx(ctx context.Context, tx *sql.Tx, record AIInvocationRecord) (int, error) {
	const ordinaryLimit = 20
	var input struct {
		Mode        string `json:"execution_mode"`
		AgentID     string `json:"agent_id"`
		TaskID      string `json:"task_id"`
		WindowLabel string `json:"window_label"`
	}
	if err := json.Unmarshal(record.RequestPayload, &input); err != nil {
		return 0, err
	}
	if (input.Mode != "agent" && input.Mode != "auto" && input.Mode != "team") || input.AgentID == "" || input.TaskID == "" || input.WindowLabel == "" {
		return ordinaryLimit, nil
	}
	if err := validateNativeAgentExecutionTx(ctx, tx, record.UserID, record.SpaceID, record.RequestPayload); err != nil {
		return 0, err
	}
	return 120, nil
}

// ScreenPlanningNode reports a model call made by a screen loop's planner
// rather than by the agent itself.
func ScreenPlanningNode(nodeID string) bool {
	return strings.HasPrefix(nodeID, "model:screen:")
}

// CompactionNode reports a model call that summarized older steps of a run
// to keep it within the model's context window.
func CompactionNode(nodeID string) bool {
	return strings.HasPrefix(nodeID, "model:compact:")
}

// TurnFreeModelNode reports a metered model call that does not spend one of
// the run's model turns.
func TurnFreeModelNode(nodeID string) bool {
	return ScreenPlanningNode(nodeID) || CompactionNode(nodeID)
}
