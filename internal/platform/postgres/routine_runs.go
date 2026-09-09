package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agenttools"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type RoutineCallBinding struct {
	StepID   string `json:"stepId"`
	CallID   string `json:"callId"`
	ToolName string `json:"toolName"`
}
type RoutineAgentToolBinding struct {
	ToolName string         `json:"toolName"`
	Action   cap.RoutinePin `json:"action"`
}
type RoutineAgentBinding struct {
	StepID        string                    `json:"stepId"`
	CallNamespace string                    `json:"callNamespace"`
	Tools         []RoutineAgentToolBinding `json:"tools"`
}
type RoutineExecution struct {
	AgentBindings []RoutineAgentBinding `json:"agentBindings,omitempty"`

	RoutineID  string               `json:"routineId"`
	Version    int                  `json:"version"`
	RunID      string               `json:"runId"`
	Definition json.RawMessage      `json:"definition"`
	Trigger    json.RawMessage      `json:"trigger"`
	Bindings   []RoutineCallBinding `json:"bindings"`
}
type RoutineAdmissionOptions struct {
	AgentModelID string
	TimedWaits   bool
}
type RoutineActiveWait struct {
	WaitID    string    `json:"waitId"`
	StepID    string    `json:"stepId"`
	Until     time.Time `json:"until"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type RoutineRunRecord struct {
	Wait            *RoutineActiveWait `json:"wait,omitempty"`
	RequestID       string             `json:"requestId"`
	Execution       RoutineExecution   `json:"execution"`
	State           string             `json:"state"`
	CancelRequested bool               `json:"cancelRequested"`
	Outcome         string             `json:"outcome"`
	Report          json.RawMessage    `json:"report"`
}

func (db *Database) AdmitManualRoutine(ctx context.Context, userID, routineID, requestID string, version int, trigger json.RawMessage, options ...RoutineAdmissionOptions) (*RoutineRunRecord, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if !cap.ValidID(routineID) || !cap.ValidID(requestID) || version < 1 || version > 2147483647 || len(trigger) > 1<<20 {
		return nil, cap.ErrInvalid
	}
	var config RoutineAdmissionOptions
	if len(options) > 1 {
		return nil, cap.ErrInvalid
	}
	if len(options) == 1 {
		config = options[0]
	}
	var triggerValue any
	if cap.Decode(trigger, &triggerValue) != nil {
		return nil, cap.ErrInvalid
	}
	var result *RoutineRunRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "routine:request:"+userID+":"+requestID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT invocation_id FROM misty_routine_runs WHERE user_id=$1 AND request_id=$2`, userID, requestID).Scan(&existing)
		if err == nil {
			result, err = routineRunTx(ctx, tx, userID, existing)
			if err != nil {
				return err
			}
			if result.Execution.RoutineID != routineID || result.Execution.Version != version || !cap.EqualJSON(result.Execution.Trigger, trigger) {
				return ErrSpaceConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "routine:active:"+userID+":"+routineID); err != nil {
			return err
		}
		draft, err := routineDraftTx(ctx, tx, userID, routineID, version)
		if err != nil {
			return err
		}
		definition, normalized, err := cap.ParseRoutineDefinition(draft.Definition)
		if err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_routine_runs r JOIN ai_invocations i ON i.id=r.invocation_id WHERE r.user_id=$1 AND r.routine_id=$2 AND (i.state NOT IN ('completed','failed','canceled') OR r.outcome='uncertain'))`, userID, routineID).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrSpaceConflict
		}
		execution := RoutineExecution{RoutineID: routineID, Version: version, RunID: "invocation_" + uuid.NewString(), Definition: normalized, Trigger: trigger, Bindings: []RoutineCallBinding{}}
		pins := []AgentSDKCapabilityBinding{}

		for _, step := range definition.Steps {
			if step.Kind == "wait" {
				if !config.TimedWaits {
					return ErrSDKProviderUnavailable
				}
				continue
			}
			actions := step.Actions
			if step.Kind == "capability" && step.Action != nil {
				actions = []cap.RoutinePin{*step.Action}
			} else if step.Kind != "agent" {
				return ErrSDKProviderUnavailable
			}
			if step.Kind == "agent" && (config.AgentModelID == "" || len(config.AgentModelID) > 240) {
				return ErrSDKProviderUnavailable
			}
			agentBinding := RoutineAgentBinding{StepID: step.ID, CallNamespace: uuid.NewString(), Tools: []RoutineAgentToolBinding{}}
			seen := map[string]bool{}
			for _, pin := range actions {
				bound, err := resolveSDKBoundCapabilityTx(ctx, tx, userID, pin.TargetID, pin.TargetRevision, pin.Capability, pin.CapabilityVersion)
				if err != nil {
					return err
				}
				if bound.Target.SpaceID != definition.SpaceID || bound.Provider.ID != pin.ProviderID || bound.Provider.Version != pin.ProviderVersion || bound.Provider.Route.Kind != "backend" {
					return ErrAppRuntimeForbidden
				}
				binding := agenttools.ProviderBinding{TargetID: pin.TargetID, TargetRevision: pin.TargetRevision, Capability: pin.Capability, CapabilityVersion: pin.CapabilityVersion, ProviderID: pin.ProviderID, ProviderVersion: pin.ProviderVersion}
				name := agenttools.ProviderToolName(binding)
				if step.Kind == "capability" {
					execution.Bindings = append(execution.Bindings, RoutineCallBinding{StepID: step.ID, CallID: uuid.NewString(), ToolName: name})
				} else if !seen[name] {
					agentBinding.Tools = append(agentBinding.Tools, RoutineAgentToolBinding{ToolName: name, Action: pin})
					seen[name] = true
				}
				pins = append(pins, AgentSDKCapabilityBinding{TargetID: pin.TargetID, TargetRevision: pin.TargetRevision, Capability: pin.Capability, CapabilityVersion: pin.CapabilityVersion, ProviderID: pin.ProviderID, ProviderVersion: pin.ProviderVersion})
			}
			if step.Kind == "agent" {
				execution.AgentBindings = append(execution.AgentBindings, agentBinding)
			}
		}

		// This payload is control-plane-owned. Generic AI input cannot select a routine.
		payload := json.RawMessage(`{"_misty_routine_execution":true}`)
		if len(execution.AgentBindings) > 0 {
			payload, _ = json.Marshal(map[string]any{"_misty_routine_execution": true, "model_id": config.AgentModelID})
		}
		record, created, err := createAIInvocationRecordTx(ctx, tx, AIInvocationRecord{ID: execution.RunID, UserID: userID, SpaceID: definition.SpaceID, SurfaceID: "routine", Mode: "quick", Trigger: "object", State: "queued", IdempotencyKey: "routine:" + requestID, RequestPayload: payload, ExpiresAt: time.Now().Add(24 * time.Hour), DispatchRuntime: true})
		if err != nil {
			return err
		}
		if !created {
			return ErrSpaceConflict
		}
		for _, pin := range pins {
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_sdk_capability_bindings(run_id,user_id,target_id,target_revision,capability,capability_version,provider_id,provider_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, record.ID, userID, pin.TargetID, pin.TargetRevision, pin.Capability, pin.CapabilityVersion, pin.ProviderID, pin.ProviderVersion); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET model_turn_limit=$2,execution_limit_ms=$3 WHERE id=$1`, record.ID, definition.Budget.ModelTurns, definition.Budget.ActiveSeconds*1000); err != nil {
			return err
		}
		raw, err := json.Marshal(execution)
		if err != nil || cap.ValidateRoutineEnvelope(raw) != nil {
			return cap.ErrInvalid
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO misty_routine_runs(user_id,routine_id,version,request_id,invocation_id,execution) VALUES($1,$2,$3,$4,$5,$6)`, userID, routineID, version, requestID, record.ID, raw); err != nil {
			return err
		}

		for _, binding := range execution.AgentBindings {
			turns := 0
			for _, step := range definition.Steps {
				if step.ID == binding.StepID {
					turns = step.MaxTurns
					break
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO misty_routine_agent_steps(user_id,invocation_id,step_id,call_namespace,model_id,max_turns) VALUES($1,$2,$3,$4,$5,$6)`, userID, record.ID, binding.StepID, binding.CallNamespace, config.AgentModelID, turns); err != nil {
				return err
			}
		}
		for _, step := range definition.Steps {
			if step.Kind == "wait" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO misty_routine_waits(user_id,invocation_id,step_id,wait_id) VALUES($1,$2,$3,$4)`, userID, record.ID, step.ID, cap.RoutineWaitID(userID, record.ID, step.ID)); err != nil {
					return err
				}
			}
		}
		result, err = routineRunTx(ctx, tx, userID, record.ID)
		return err
	})
	return result, err
}
func routineRunTx(ctx context.Context, tx *sql.Tx, userID, runID string, recovery ...bool) (*RoutineRunRecord, error) {
	var out RoutineRunRecord
	var execution []byte
	var spaceID string
	err := tx.QueryRowContext(ctx, `SELECT r.request_id,r.execution,i.state,r.cancel_requested_at IS NOT NULL,r.outcome,COALESCE(r.report,'null'::jsonb),COALESCE(i.space_id,'') FROM misty_routine_runs r JOIN ai_invocations i ON i.id=r.invocation_id AND i.user_id=r.user_id WHERE r.user_id=$1 AND r.invocation_id=$2`, userID, runID).Scan(&out.RequestID, &execution, &out.State, &out.CancelRequested, &out.Outcome, &out.Report, &spaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(recovery) == 0 || !recovery[0] {
		if err := sdkTargetSpaceAccessTx(ctx, tx, userID, spaceID); err != nil {
			return nil, err
		}
	}
	if cap.Decode(execution, &out.Execution) != nil {
		return nil, ErrSpaceInvalid
	}
	var active RoutineActiveWait
	err = tx.QueryRowContext(ctx, `SELECT wait_id,step_id,until_at,expires_at FROM misty_routine_waits WHERE user_id=$1 AND invocation_id=$2 AND state='waiting'`, userID, runID).Scan(&active.WaitID, &active.StepID, &active.Until, &active.ExpiresAt)
	if err == nil {
		out.Wait = &active
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &out, nil
}
func (db *Database) RoutineRun(ctx context.Context, userID, runID string) (*RoutineRunRecord, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	var out *RoutineRunRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error { var err error; out, err = routineRunTx(ctx, tx, userID, runID); return err })
	return out, err
}

type RoutineEffectReceipt struct {
	State      string
	Ciphertext []byte
	ToolName   string
}

func (db *Database) RoutineEffectReceipts(ctx context.Context, userID, runID string) (map[string]RoutineEffectReceipt, error) {
	receipts := map[string]RoutineEffectReceipt{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT j.idempotency_key,j.state,j.result_ciphertext,j.tool_name FROM agent_toolbox_action_journal j JOIN misty_routine_runs r ON r.invocation_id=j.run_id AND r.user_id=j.user_id WHERE j.user_id=$1 AND j.run_id=$2`, userID, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var receipt RoutineEffectReceipt
			if err := rows.Scan(&key, &receipt.State, &receipt.Ciphertext, &receipt.ToolName); err != nil {
				return err
			}
			receipts[key] = receipt
		}
		return rows.Err()
	})
	return receipts, err
}

func (db *Database) PublishRoutineCompletion(ctx context.Context, userID, runID, outcome string, report json.RawMessage) error {
	if outcome != "completed" && outcome != "partial" && outcome != "failed" && outcome != "uncertain" && outcome != "cancelled" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`, runID, userID).Scan(&current); err != nil {
			return err
		}
		var previous string
		if err := tx.QueryRowContext(ctx, `SELECT outcome FROM misty_routine_runs WHERE invocation_id=$1 AND user_id=$2`, runID, userID).Scan(&previous); err != nil {
			return err
		}
		if previous != "" {
			return nil
		}
		// Claims serialize on this same run row. Recheck the evidence at publication
		// so an in-flight action can never become a cancellation or success label.
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_toolbox_action_journal WHERE user_id=$1 AND run_id=$2 AND state IN ('started','unknown')`, userID, runID).Scan(&pending); err != nil {
			return err
		}
		var value map[string]any
		if json.Unmarshal(report, &value) != nil {
			return ErrSpaceInvalid
		}
		if pending > 0 {
			outcome = "uncertain"
			value["state"] = "uncertain"
			rows, err := tx.QueryContext(ctx, `SELECT idempotency_key FROM agent_toolbox_action_journal WHERE user_id=$1 AND run_id=$2 AND state IN ('started','unknown')`, userID, runID)
			if err != nil {
				return err
			}
			keys := map[string]bool{}
			for rows.Next() {
				var key string
				if err := rows.Scan(&key); err != nil {
					rows.Close()
					return err
				}
				keys[key] = true
			}
			scanErr := rows.Err()
			rows.Close()
			if scanErr != nil {
				return scanErr
			}
			uncertainAgents := map[string]bool{}
			agentRows, err := tx.QueryContext(ctx, `SELECT DISTINCT c.step_id FROM misty_routine_agent_calls c JOIN agent_toolbox_action_journal j ON j.idempotency_key='sdk-agent-effect:'||c.effect_id::text AND j.user_id=c.user_id AND j.run_id=c.invocation_id WHERE c.user_id=$1 AND c.invocation_id=$2 AND j.state IN ('started','unknown')`, userID, runID)
			if err != nil {
				return err
			}
			for agentRows.Next() {
				var id string
				if err := agentRows.Scan(&id); err != nil {
					agentRows.Close()
					return err
				}
				uncertainAgents[id] = true
			}
			scanErr = agentRows.Err()
			agentRows.Close()
			if scanErr != nil {
				return scanErr
			}
			if steps, ok := value["steps"].([]any); ok {
				for _, entry := range steps {
					if step, ok := entry.(map[string]any); ok {
						if id, ok := step["stepId"].(string); ok && uncertainAgents[id] {
							step["state"] = "uncertain"
						}
						if callID, ok := step["callId"].(string); ok {
							_, effectID := cap.AgentSDKIdentities(userID, runID, callID)
							if keys["sdk-agent-effect:"+effectID] {
								step["state"] = "uncertain"
							}
						}
					}
				}
			}
		}
		if outcome == "completed" {
			var expected int
			steps, ok := value["steps"].([]any)
			if !ok {
				return ErrSpaceInvalid
			}
			for _, entry := range steps {
				step, ok := entry.(map[string]any)
				if !ok {
					return ErrSpaceInvalid
				}
				if step["state"] == "skipped" {
					continue
				}
				if step["state"] != "completed" {
					return ErrSpaceConflict
				}
				callID, ok := step["callId"].(string)
				if !ok {
					return ErrSpaceInvalid
				}
				var waited bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_routine_waits WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3 AND wait_id::text=$4 AND state='completed')`, userID, runID, step["stepId"], callID).Scan(&waited); err != nil {
					return err
				}
				if waited {
					continue
				}
				// An agent checkpoint contributes its actual capability effects,
				// never a fabricated SDK effect for its logical step namespace.
				var agentState string
				var protected []byte
				agentErr := tx.QueryRowContext(ctx, `SELECT state,output_ciphertext FROM misty_routine_agent_steps WHERE user_id=$1 AND invocation_id=$2 AND call_namespace::text=$3 AND step_id=$4`, userID, runID, callID, step["stepId"]).Scan(&agentState, &protected)
				if agentErr == nil {
					if agentState != "completed" || len(protected) == 0 {
						return ErrSpaceConflict
					}
					var calls, unconfirmed int
					if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE j.state IS DISTINCT FROM 'completed' OR j.result_ciphertext IS NULL) FROM misty_routine_agent_calls c LEFT JOIN agent_toolbox_action_journal j ON j.idempotency_key='sdk-agent-effect:'||c.effect_id::text AND j.user_id=c.user_id AND j.run_id=c.invocation_id AND j.tool_name=c.tool_name WHERE c.user_id=$1 AND c.invocation_id=$2 AND c.step_id=$3`, userID, runID, step["stepId"]).Scan(&calls, &unconfirmed); err != nil {
						return err
					}
					if unconfirmed > 0 {
						return ErrSpaceConflict
					}
					expected += calls
					continue
				}
				if !errors.Is(agentErr, sql.ErrNoRows) {
					return agentErr
				}
				_, effectID := cap.AgentSDKIdentities(userID, runID, callID)
				var confirmed bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_toolbox_action_journal WHERE user_id=$1 AND run_id=$2 AND idempotency_key=$3 AND state='completed' AND result_ciphertext IS NOT NULL)`, userID, runID, "sdk-agent-effect:"+effectID).Scan(&confirmed); err != nil {
					return err
				}
				if !confirmed {
					return ErrSpaceConflict
				}
				expected++
			}
			var actual int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_toolbox_action_journal WHERE user_id=$1 AND run_id=$2`, userID, runID).Scan(&actual); err != nil {
				return err
			}
			if actual != expected {
				return ErrSpaceConflict
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_waits SET state='cancelled',updated_at=NOW() WHERE user_id=$1 AND invocation_id=$2 AND state='waiting'`, userID, runID); err != nil {
			return err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		state := "failed"
		if outcome == "completed" {
			state = "completed"
		}
		if outcome == "cancelled" {
			state = "canceled"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_runs SET outcome=$3,report=$4 WHERE user_id=$1 AND invocation_id=$2`, userID, runID, outcome, encoded); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state=$3,approval_wait_id='',updated_at=NOW() WHERE user_id=$1 AND id=$2`, userID, runID, state); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"type": "invocation." + state, "state": state, "routine_outcome": outcome, "routine_report": value})
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) SELECT $1,COALESCE(MAX(sequence),0)+1,$2,$3,'routine:completion' FROM ai_invocation_events WHERE invocation_id=$1 ON CONFLICT DO NOTHING`, runID, "invocation."+state, payload)
		return err
	})
}
func (db *Database) RequestRoutineCancellation(ctx context.Context, userID, runID string) error {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return ErrAppRuntimeForbidden
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state, runtimeID string
		if err := tx.QueryRowContext(ctx, `SELECT state,runtime_run_id FROM ai_invocations WHERE id=$1 AND user_id=$2 AND surface_id='routine' FOR UPDATE`, runID, userID).Scan(&state, &runtimeID); err != nil {
			return err
		}
		if state == "completed" || state == "failed" || state == "canceled" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_runs SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()) WHERE invocation_id=$1 AND user_id=$2`, runID, userID); err != nil {
			return err
		}
		if runtimeID != "" {
			if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
				return err
			}
			return queueAgentContinuationTx(ctx, tx, userID, runID, "runtime.cancel", runtimeID, AgentContinuation{RuntimeID: runtimeID})
		}
		// No runtime was activated, so no capability could have entered execution.
		// Activation locks this row and rejects the now-terminal admission.
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',updated_at=NOW() WHERE id=$1 AND user_id=$2`, runID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_runs SET outcome='cancelled',report='{"state":"cancelled","steps":[]}'::jsonb WHERE invocation_id=$1 AND user_id=$2`, runID, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) SELECT $1,COALESCE(MAX(sequence),0)+1,'invocation.canceled','{"type":"invocation.canceled","state":"canceled","routine_outcome":"cancelled"}'::jsonb,'routine:completion' FROM ai_invocation_events WHERE invocation_id=$1 ON CONFLICT DO NOTHING`, runID)
		return err
	})
}

// Runtime recovery may settle existing effects after Space access is revoked.
// It grants no tool access and is not exposed through user/app read controls.
func (db *Database) RoutineRunForRecovery(ctx context.Context, userID, runID string) (*RoutineRunRecord, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	var out *RoutineRunRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		out, err = routineRunTx(ctx, tx, userID, runID, true)
		return err
	})
	return out, err
}
