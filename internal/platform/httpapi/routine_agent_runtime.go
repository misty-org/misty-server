package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type routineAgentResult struct {
	State           string          `json:"state"`
	Output          json.RawMessage `json:"output,omitempty"`
	Code            string          `json:"code,omitempty"`
	ModelTurns      int             `json:"modelTurns"`
	CapabilityCalls int             `json:"capabilityCalls"`
	Usage           json.RawMessage `json:"usage,omitempty"`
}
type routineAgentRequest struct {
	RuntimeRunID string             `json:"runtime_run_id"`
	Phase        string             `json:"phase"`
	StepID       string             `json:"step_id"`
	NodeID       string             `json:"node_id,omitempty"`
	Result       routineAgentResult `json:"result,omitempty"`
	Usage        json.RawMessage    `json:"usage,omitempty"`
}

// This signed worker endpoint owns checkpoint admission. SDK bearers and model
// tool calls cannot open steps, spend model turns, or attest to their completion.
func (s *SpacesService) AgentRuntimeRoutineAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body routineAgentRequest
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		record, err := s.database.AIInvocationRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if record.SurfaceID != "routine" {
			writeAgentError(w, db.ErrSpaceForbidden)
			return
		}
		if body.Phase == "model_finish" {
			// A late cost receipt is safe after revocation/cancellation. No output, prompt
			// or permission is accepted on this path, only a previously claimed node.
			err = s.finishRoutineModel(r.Context(), record, body.NodeID, body.Usage)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"recorded": true})
			return
		}
		run, err := s.database.RoutineRun(r.Context(), record.UserID, record.ID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if record.State != "running" || run.CancelRequested || run.Outcome != "" || !record.ExpiresAt.After(time.Now()) {
			writeAgentError(w, db.ErrSpaceConflict)
			return
		}
		if body.Phase == "model_start" {
			namespace, _, valid := cap.RoutineAgentModelNode(body.NodeID)
			if !valid {
				writeAgentError(w, db.ErrSpaceInvalid)
				return
			}
			for _, binding := range run.Execution.AgentBindings {
				if binding.CallNamespace == namespace {
					body.StepID = binding.StepID
				}
			}
		}
		definition, _, err := cap.ParseRoutineDefinition(run.Execution.Definition)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		var step *cap.RoutineStep
		for i := range definition.Steps {
			if definition.Steps[i].ID == body.StepID && definition.Steps[i].Kind == "agent" {
				step = &definition.Steps[i]
			}
		}
		if step == nil {
			writeAgentError(w, db.ErrSpaceInvalid)
			return
		}
		checkpoints, err := s.database.RoutineAgentCheckpoints(r.Context(), record.UserID, record.ID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		var checkpoint *db.RoutineAgentCheckpoint
		for i := range checkpoints {
			if checkpoints[i].StepID == step.ID {
				checkpoint = &checkpoints[i]
			}
		}
		if checkpoint == nil {
			writeAgentError(w, db.ErrSpaceConflict)
			return
		}
		terminal := checkpoint.State != "pending" && checkpoint.State != "running"
		_, next, err := s.routineProgress(r.Context(), record, run)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if !terminal && (next == nil || next.Agent == nil || next.Binding.StepID != step.ID) {
			writeAgentError(w, db.ErrSpaceConflict)
			return
		}
		var response any
		switch body.Phase {
		case "open":
			if terminal {
				var raw json.RawMessage
				raw, err = s.restoreAgentEffectResult("routine-agent:"+checkpoint.CallNamespace+":outcome", checkpoint.Ciphertext)
				response = map[string]any{"state": "replayed", "outcome": raw}
			} else {
				var prompt string
				if json.Unmarshal(next.Input, &prompt) != nil || len(prompt) == 0 || len(prompt) > 65536 {
					writeAgentError(w, db.ErrSpaceInvalid)
					return
				}
				// Availability is rechecked before starting a model, not inferred from a
				// cached registration captured at admission.
				if _, err = s.prepareRoutineRuntime(r.Context(), record); err == nil {
					checkpoint, err = s.database.OpenRoutineAgent(r.Context(), record.UserID, record.ID, step.ID, record.RuntimeRunID)
				}
				if err == nil {
					response = map[string]any{"state": "running", "protocol": 1, "stepId": step.ID, "callNamespace": checkpoint.CallNamespace, "prompt": prompt, "modelId": checkpoint.ModelID, "maxTurns": checkpoint.MaxTurns}
				}
			}
		case "finish":
			response, err = s.finishRoutineAgent(r.Context(), record, *step, *checkpoint, body.Result, false)
		case "model_start":
			namespace, _, valid := cap.RoutineAgentModelNode(body.NodeID)
			if terminal || !valid || namespace != checkpoint.CallNamespace {
				writeAgentError(w, db.ErrSpaceForbidden)
				return
			}
			if _, err = s.prepareRoutineRuntime(r.Context(), record); err == nil {
				err = s.database.ReserveAgentModelTurn(r.Context(), record.UserID, record.ID, record.RuntimeRunID, body.NodeID)
			}
			if err == nil && s.usageMeter != nil {
				_, err = serveragent.ReserveUsage(s.usageMeter, record.UserID, record.SpaceID, routineModelUsageKey(record.ID, body.NodeID), "assistant_ai", "ai-gateway", checkpoint.ModelID, 32000, serveragent.MaxModelOutputTokens)
			}
			if err == nil {
				response, err = s.database.AgentRunExecutionBudget(r.Context(), record.UserID, record.ID, record.RuntimeRunID, true)
			}
		default:
			err = db.ErrSpaceInvalid
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, response)
	}
}
func routineModelUsageKey(runID, nodeID string) string {
	return "agent-runtime:" + runID + ":" + nodeID
}
func (s *SpacesService) finishRoutineModel(ctx context.Context, record *db.AIInvocationRecord, nodeID string, raw json.RawMessage) error {
	if len(raw) > 8192 || !json.Valid(raw) {
		return db.ErrSpaceInvalid
	}
	usage := agentRuntimeModelUsage(raw)
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens < 0 || usage.ReasoningTokens < 0 || usage.CachedInputTokens > usage.InputTokens || usage.ReasoningTokens > usage.OutputTokens || usage.InputTokens > 2000000 || usage.OutputTokens > 100000 {
		return db.ErrSpaceInvalid
	}
	encoded, _ := json.Marshal(usage)
	if err := s.database.RecordRoutineModelUsage(ctx, record.UserID, record.ID, record.RuntimeRunID, nodeID, encoded); err != nil {
		return err
	}
	if s.usageMeter == nil {
		return nil
	}
	key := routineModelUsageKey(record.ID, nodeID)
	model := aiInvocationMeteredModel(record)
	reservation, err := serveragent.ReserveUsage(s.usageMeter, record.UserID, record.SpaceID, key, "assistant_ai", "ai-gateway", model, 32000, serveragent.MaxModelOutputTokens)
	if err != nil {
		return err
	}
	if usage.Estimated {
		return s.usageMeter.Release(reservation)
	}
	_, err = s.usageMeter.Settle(reservation, key+":settle", "assistant_ai", "ai-gateway", model, usage)
	return err
}

// Result fields are an untrusted report. The journal supplies confirmation and
// uncertainty; the declared output schema gates data passed to dependent steps.
func evaluateRoutineAgentResult(step cap.RoutineStep, checkpoint db.RoutineAgentCheckpoint, result routineAgentResult, receipts []db.RoutineAgentCallReceipt, restore func(string, []byte) (json.RawMessage, error), turns, finished int, recovery bool) (string, json.RawMessage, error) {
	if !recovery && result.State != "completed" && result.State != "failed" && result.State != "partial" && result.State != "uncertain" {
		return "", nil, db.ErrSpaceInvalid
	}
	state := "failed"
	confirmed, unconfirmed, uncertain, partial := false, false, false, false
	effectID := checkpoint.CallNamespace
	for _, receipt := range receipts {
		switch receipt.State {
		case "started", "unknown":
			uncertain = true
			effectID = receipt.EffectID
		case "completed":
			raw, err := restore(receipt.EffectID+":outcome", receipt.Ciphertext)
			if err != nil {
				return "", nil, err
			}
			var outcome cap.BackendOutcome
			if json.Unmarshal(raw, &outcome) != nil || outcome.Status != "success" {
				unconfirmed = true
				continue
			}
			confirmed = true
			partial = partial || outcome.Partial
		default:
			unconfirmed = true
		}
	}
	validOutput := false
	if !recovery && result.State == "completed" && len(result.Output) > 0 && len(result.Output) <= 512<<10 && cap.ValidateRoutineEnvelope(result.Output) == nil {
		schema, err := cap.CompileSchema(step.OutputSchema)
		if err != nil {
			return "", nil, err
		}
		var output any
		validOutput = json.Unmarshal(result.Output, &output) == nil && schema.Validate(output) == nil
	}
	switch {
	case uncertain || result.State == "uncertain":
		state = "uncertain"
	case partial:
		state = "partial"
	case !recovery && result.State == "completed" && validOutput && !unconfirmed && turns > 0 && turns == finished && turns == result.ModelTurns && turns <= checkpoint.MaxTurns && len(receipts) == result.CapabilityCalls:
		state = "completed"
	case confirmed:
		state = "partial"
	}
	var value any
	switch state {
	case "completed":
		value = map[string]any{"status": "success", "result": result.Output, "partial": false, "evidence": []any{}}
	case "partial":
		value = map[string]any{"status": "success", "result": nil, "partial": true, "evidence": []any{}}
	case "uncertain":
		value = map[string]any{"status": "uncertain", "effectId": effectID, "reason": "A routine agent action could not be confirmed. Review the recorded effects before recovery.", "evidence": []any{}}
	default:
		value = map[string]any{"status": "failure", "code": "routine_agent_incomplete", "message": "The agent step did not produce a confirmed result matching its declared output.", "retryable": false}
	}
	encoded, err := json.Marshal(value)
	return state, encoded, err
}
func (s *SpacesService) finishRoutineAgent(ctx context.Context, record *db.AIInvocationRecord, step cap.RoutineStep, checkpoint db.RoutineAgentCheckpoint, result routineAgentResult, recovery bool) (json.RawMessage, error) {
	receipts, err := s.database.RoutineAgentCallReceipts(ctx, record.UserID, record.ID, step.ID)
	if err != nil {
		return nil, err
	}
	turns, finished, err := s.database.RoutineAgentModelCounts(ctx, record.UserID, record.ID, checkpoint.CallNamespace)
	if err != nil {
		return nil, err
	}
	state, outcome, err := evaluateRoutineAgentResult(step, checkpoint, result, receipts, s.restoreAgentEffectResult, turns, finished, recovery)
	if err != nil {
		return nil, err
	}
	// Canonical fingerprint binds lost-ack retries to the same model report.
	encoded, _ := json.Marshal(result)
	var canonical any
	if cap.Decode(encoded, &canonical) != nil {
		return nil, db.ErrSpaceInvalid
	}
	encoded, _ = json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	aad := "routine-agent:" + checkpoint.CallNamespace + ":outcome"
	cipher, err := s.protectAgentEffectResult(aad, outcome)
	if err != nil {
		return nil, err
	}
	stored, err := s.database.FinishRoutineAgent(ctx, record.UserID, record.ID, step.ID, record.RuntimeRunID, state, hex.EncodeToString(digest[:]), cipher, len(receipts), turns, recovery)
	if err != nil {
		return nil, err
	}
	return s.restoreAgentEffectResult(aad, stored.Ciphertext)
}
func (s *SpacesService) recoverRoutineAgents(ctx context.Context, record *db.AIInvocationRecord, run *db.RoutineRunRecord) error {
	checkpoints, err := s.database.RoutineAgentCheckpoints(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	definition, _, err := cap.ParseRoutineDefinition(run.Execution.Definition)
	if err != nil {
		return err
	}
	for _, checkpoint := range checkpoints {
		if checkpoint.State != "running" {
			continue
		}
		for _, step := range definition.Steps {
			if step.ID == checkpoint.StepID {
				if _, err = s.finishRoutineAgent(ctx, record, step, checkpoint, routineAgentResult{State: "failed"}, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
