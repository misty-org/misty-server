package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type RoutineAgentCheckpoint struct {
	StepID, CallNamespace, ModelID, State string
	MaxTurns                              int
	Ciphertext                            []byte
}
type RoutineAgentCallReceipt struct {
	CallID, ToolName, EffectID, State string
	Ciphertext                        []byte
}

func routineAgentRunLockTx(ctx context.Context, tx *sql.Tx, userID, runID, runtimeID string, allowWait bool) error {
	var state, pinned, spaceID string
	var cancelled, live bool
	if err := tx.QueryRowContext(ctx, `SELECT i.state,i.runtime_run_id,COALESCE(i.space_id,''),r.cancel_requested_at IS NOT NULL,i.expires_at>NOW() FROM ai_invocations i JOIN misty_routine_runs r ON r.invocation_id=i.id AND r.user_id=i.user_id WHERE i.id=$1 AND i.user_id=$2 FOR UPDATE OF i`, runID, userID).Scan(&state, &pinned, &spaceID, &cancelled, &live); err != nil {
		return err
	}
	if pinned != runtimeID || !live || cancelled || (state != "running" && !(allowWait && state == "awaiting_approval")) {
		return ErrSpaceConflict
	}
	return sdkTargetSpaceAccessTx(ctx, tx, userID, spaceID)
}
func routineAgentCheckpointTx(ctx context.Context, tx *sql.Tx, userID, runID, stepID string) (*RoutineAgentCheckpoint, error) {
	var out RoutineAgentCheckpoint
	err := tx.QueryRowContext(ctx, `SELECT step_id,call_namespace,model_id,max_turns,state,output_ciphertext FROM misty_routine_agent_steps WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3`, userID, runID, stepID).Scan(&out.StepID, &out.CallNamespace, &out.ModelID, &out.MaxTurns, &out.State, &out.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return &out, err
}
func (db *Database) RoutineAgentCheckpoints(ctx context.Context, userID, runID string) ([]RoutineAgentCheckpoint, error) {
	out := []RoutineAgentCheckpoint{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT step_id,call_namespace,model_id,max_turns,state,output_ciphertext FROM misty_routine_agent_steps WHERE user_id=$1 AND invocation_id=$2 ORDER BY step_id`, userID, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RoutineAgentCheckpoint
			if err := rows.Scan(&item.StepID, &item.CallNamespace, &item.ModelID, &item.MaxTurns, &item.State, &item.Ciphertext); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
func (db *Database) OpenRoutineAgent(ctx context.Context, userID, runID, stepID, runtimeID string) (*RoutineAgentCheckpoint, error) {
	var out *RoutineAgentCheckpoint
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := routineAgentRunLockTx(ctx, tx, userID, runID, runtimeID, false); err != nil {
			return err
		}
		checkpoint, err := routineAgentCheckpointTx(ctx, tx, userID, runID, stepID)
		if err != nil {
			return err
		}
		if checkpoint.State == "pending" {
			if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_agent_steps SET state='running',updated_at=NOW() WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3 AND state='pending'`, userID, runID, stepID); err != nil {
				return err
			}
			checkpoint.State = "running"
		}
		out = checkpoint
		return nil
	})
	return out, err
}
func (db *Database) AdmitRoutineAgentCall(ctx context.Context, userID, runID, runtimeID, stepID, callID, toolName string, input json.RawMessage, allowNew bool) error {
	namespace, ok := cap.RoutineAgentCallNamespace(callID)
	if !ok {
		return ErrSpaceInvalid
	}
	var value any
	if cap.Decode(input, &value) != nil {
		return ErrSpaceInvalid
	}
	canonical, _ := json.Marshal(value)
	digest := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(digest[:])
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := routineAgentRunLockTx(ctx, tx, userID, runID, runtimeID, true); err != nil {
			return err
		}
		checkpoint, err := routineAgentCheckpointTx(ctx, tx, userID, runID, stepID)
		if err != nil {
			return err
		}
		if checkpoint.CallNamespace != namespace {
			return ErrAppRuntimeForbidden
		}
		var pinnedTool bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_routine_runs r, jsonb_array_elements(r.execution->'agentBindings') b, jsonb_array_elements(b->'tools') t WHERE r.user_id=$1 AND r.invocation_id=$2 AND b->>'stepId'=$3 AND b->>'callNamespace'=$4 AND t->>'toolName'=$5)`, userID, runID, stepID, namespace, toolName).Scan(&pinnedTool); err != nil {
			return err
		}
		if !pinnedTool {
			return ErrAppRuntimeForbidden
		}

		var previousStep, previousTool, previousHash string
		err = tx.QueryRowContext(ctx, `SELECT step_id,tool_name,arguments_fingerprint FROM misty_routine_agent_calls WHERE user_id=$1 AND invocation_id=$2 AND call_id=$3`, userID, runID, callID).Scan(&previousStep, &previousTool, &previousHash)
		if err == nil {
			if previousStep != stepID || previousTool != toolName || previousHash != fingerprint {
				return ErrSpaceConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !allowNew || checkpoint.State != "running" {
			return ErrSpaceConflict
		}
		if err := routineAgentRunLockTx(ctx, tx, userID, runID, runtimeID, false); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM misty_routine_agent_calls WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3`, userID, runID, stepID).Scan(&count); err != nil {
			return err
		}
		if count >= cap.RoutineAgentMaxCalls {
			return ErrSpaceLimit
		}
		_, effectID := cap.AgentSDKIdentities(userID, runID, callID)
		_, err = tx.ExecContext(ctx, `INSERT INTO misty_routine_agent_calls(user_id,invocation_id,step_id,call_id,tool_name,arguments_fingerprint,effect_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, userID, runID, stepID, callID, toolName, fingerprint, effectID)
		return err
	})
}
func (db *Database) RoutineAgentCallReceipts(ctx context.Context, userID, runID, stepID string) ([]RoutineAgentCallReceipt, error) {
	out := []RoutineAgentCallReceipt{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.call_id,c.tool_name,c.effect_id,COALESCE(j.state,''),j.result_ciphertext FROM misty_routine_agent_calls c LEFT JOIN agent_toolbox_action_journal j ON j.idempotency_key='sdk-agent-effect:'||c.effect_id::text AND j.user_id=c.user_id AND j.run_id=c.invocation_id AND j.tool_name=c.tool_name WHERE c.user_id=$1 AND c.invocation_id=$2 AND c.step_id=$3 ORDER BY c.created_at,c.call_id`, userID, runID, stepID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RoutineAgentCallReceipt
			if err := rows.Scan(&item.CallID, &item.ToolName, &item.EffectID, &item.State, &item.Ciphertext); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
func (db *Database) FinishRoutineAgent(ctx context.Context, userID, runID, stepID, runtimeID, state, fingerprint string, ciphertext []byte, expectedCalls, expectedTurns int, recovery bool) (*RoutineAgentCheckpoint, error) {
	if state != "completed" && state != "failed" && state != "partial" && state != "uncertain" || len(ciphertext) < 17 || len(ciphertext) > 2<<20 {
		return nil, ErrSpaceInvalid
	}
	var out *RoutineAgentCheckpoint
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var runState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`, runID, userID).Scan(&runState); err != nil {
			return err
		}
		if !recovery {
			if err := routineAgentRunLockTx(ctx, tx, userID, runID, runtimeID, false); err != nil {
				return err
			}
		} else if state == "completed" {
			return ErrSpaceForbidden
		}
		checkpoint, err := routineAgentCheckpointTx(ctx, tx, userID, runID, stepID)
		if err != nil {
			return err
		}
		if checkpoint.State != "running" {
			if checkpoint.State == "pending" {
				return ErrSpaceConflict
			}
			var previous string
			if err := tx.QueryRowContext(ctx, `SELECT completion_fingerprint FROM misty_routine_agent_steps WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3`, userID, runID, stepID).Scan(&previous); err != nil {
				return err
			}
			if !recovery && previous != fingerprint {
				return ErrSpaceConflict
			}
			out = checkpoint
			return nil
		}
		var calls, unconfirmed int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE j.state IS DISTINCT FROM 'completed' OR j.result_ciphertext IS NULL) FROM misty_routine_agent_calls c LEFT JOIN agent_toolbox_action_journal j ON j.idempotency_key='sdk-agent-effect:'||c.effect_id::text AND j.user_id=c.user_id AND j.run_id=c.invocation_id AND j.tool_name=c.tool_name WHERE c.user_id=$1 AND c.invocation_id=$2 AND c.step_id=$3`, userID, runID, stepID).Scan(&calls, &unconfirmed); err != nil {
			return err
		}
		if state == "completed" {
			var turns, finished int
			if err := tx.QueryRowContext(ctx, `SELECT count(*),count(routine_usage) FROM agent_model_turn_claims WHERE user_id=$1 AND run_id=$2 AND node_id LIKE $3`, userID, runID, "model:routine:"+checkpoint.CallNamespace+":%").Scan(&turns, &finished); err != nil {
				return err
			}
			if turns != expectedTurns || turns < 1 || turns > checkpoint.MaxTurns || finished != turns {
				return ErrSpaceConflict
			}
		}
		if calls != expectedCalls || state == "completed" && unconfirmed > 0 {
			return ErrSpaceConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_agent_steps SET state=$4,completion_fingerprint=$5,output_ciphertext=$6,updated_at=NOW() WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3 AND state='running'`, userID, runID, stepID, state, fingerprint, ciphertext); err != nil {
			return err
		}
		out, err = routineAgentCheckpointTx(ctx, tx, userID, runID, stepID)
		return err
	})
	return out, err
}

// Usage acknowledgements accept only an already admitted model node. They can
// arrive after cancellation or revocation because recording cost grants no work.
func (db *Database) RecordRoutineModelUsage(ctx context.Context, userID, runID, runtimeID, nodeID string, usage json.RawMessage) error {
	if _, _, ok := cap.RoutineAgentModelNode(nodeID); !ok || len(usage) > 4096 || !json.Valid(usage) {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var pinned string
		if err := tx.QueryRowContext(ctx, `SELECT runtime_run_id FROM ai_invocations WHERE id=$1 AND user_id=$2 AND surface_id='routine' FOR UPDATE`, runID, userID).Scan(&pinned); err != nil {
			return err
		}
		if pinned != runtimeID {
			return ErrSpaceForbidden
		}
		// A newly admitted turn has SQL NULL usage until its first receipt.
		// Scan into bytes so database/sql can represent that pending state.
		var previous []byte
		if err := tx.QueryRowContext(ctx, `SELECT routine_usage FROM agent_model_turn_claims WHERE run_id=$1 AND user_id=$2 AND runtime_run_id=$3 AND node_id=$4`, runID, userID, runtimeID, nodeID).Scan(&previous); err != nil {
			return err
		}
		if len(previous) > 0 {
			if !cap.EqualJSON(previous, usage) {
				return ErrSpaceConflict
			}
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_model_turn_claims SET routine_usage=$5 WHERE run_id=$1 AND user_id=$2 AND runtime_run_id=$3 AND node_id=$4 AND routine_usage IS NULL`, runID, userID, runtimeID, nodeID, usage)
		return err
	})
}
func (db *Database) RoutineAgentModelCounts(ctx context.Context, userID, runID, namespace string) (turns, finished int, err error) {
	if !cap.ValidID(namespace) {
		return 0, 0, ErrSpaceInvalid
	}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*),count(routine_usage) FROM agent_model_turn_claims WHERE user_id=$1 AND run_id=$2 AND node_id LIKE $3`, userID, runID, "model:routine:"+namespace+":%").Scan(&turns, &finished)
	})
	return
}

// A scope mapping and a model checkpoint cannot close the admission-to-effect
// race by themselves. Recheck under the same run lock used by the effect claim.
func routineAgentEffectClaimTx(ctx context.Context, tx *sql.Tx, action AgentToolboxAction) error {
	var checkpoint string
	err := tx.QueryRowContext(ctx, `SELECT s.state FROM misty_routine_agent_calls c JOIN misty_routine_agent_steps s USING(user_id,invocation_id,step_id) WHERE c.user_id=$1 AND c.invocation_id=$2 AND 'sdk-agent-effect:'||c.effect_id::text=$3`, action.UserID, action.RunID, action.IdempotencyKey).Scan(&checkpoint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if checkpoint == "running" {
		return nil
	}
	var confirmed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_toolbox_action_journal WHERE user_id=$1 AND run_id=$2 AND idempotency_key=$3 AND state='completed' AND result_ciphertext IS NOT NULL)`, action.UserID, action.RunID, action.IdempotencyKey).Scan(&confirmed); err != nil {
		return err
	}
	if !confirmed {
		return ErrSpaceConflict
	}
	return nil
}
func routineAgentModelTurnTx(ctx context.Context, tx *sql.Tx, userID, runID, runtimeID, nodeID string) error {
	namespace, turn, ok := cap.RoutineAgentModelNode(nodeID)
	if !ok {
		return ErrSpaceForbidden
	}
	if err := routineAgentRunLockTx(ctx, tx, userID, runID, runtimeID, false); err != nil {
		return err
	}
	var state string
	var limit int
	if err := tx.QueryRowContext(ctx, `SELECT state,max_turns FROM misty_routine_agent_steps WHERE user_id=$1 AND invocation_id=$2 AND call_namespace=$3`, userID, runID, namespace).Scan(&state, &limit); err != nil {
		return err
	}
	if state != "running" || turn > limit {
		return ErrAgentModelTurnLimit
	}
	// Sequential turn identities prevent skipping a failed or unacknowledged turn.
	if turn > 1 {
		prefix := "model:routine:" + namespace + ":"
		var ready bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_model_turn_claims WHERE user_id=$1 AND run_id=$2 AND node_id=$3 AND routine_usage IS NOT NULL)`, userID, runID, prefix+fmt.Sprint(turn-1)).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return ErrSpaceConflict
		}
	}
	return nil
}
