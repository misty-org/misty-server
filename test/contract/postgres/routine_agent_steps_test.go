package db

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestRoutineAgentCheckpointClosesLateEffectsAndPinsRecovery(t *testing.T) {
	for _, lateCall := range []bool{false, true} {
		name := "confirmed replay"
		if lateCall {
			name = "admitted but undispatched effect"
		}
		t.Run(name, func(t *testing.T) {
			database, appctx, user, pin := sdkInvocationFixture(t)
			ctx := t.Context()
			id, request, runtime := uuid.NewString(), uuid.NewString(), uuid.NewString()
			action := cap.RoutinePin{Capability: pin.Capability, CapabilityVersion: pin.CapabilityVersion, ProviderID: pin.ProviderID, ProviderVersion: pin.ProviderVersion, TargetID: pin.TargetID, TargetRevision: pin.TargetRevision}
			raw, _ := json.Marshal(map[string]any{"protocol": 1, "name": "Agent proof", "trigger": map[string]any{"kind": "manual"}, "budget": map[string]any{}, "steps": []any{map[string]any{"id": "agent", "label": "Read habits", "kind": "agent", "actions": []any{action}, "maxTurns": 2, "prompt": map[string]any{"kind": "literal", "value": "Summarize habits"}, "outputSchema": map[string]any{"type": "object"}}}})
			if _, err := database.SaveRoutineDraft(ctx, user, id, 0, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := database.AdmitManualRoutine(appctx, user, id, request, 1, []byte(`{}`), RoutineAdmissionOptions{AgentModelID: "model-pinned"}); !errors.Is(err, ErrAppRuntimeForbidden) {
				t.Fatalf("app admission: %v", err)
			}
			if _, err := database.AdmitManualRoutine(ctx, user, id, request, 1, []byte(`{}`)); err == nil {
				t.Fatal("agent admitted without enabled model")
			}
			run, err := database.AdmitManualRoutine(ctx, user, id, request, 1, []byte(`{}`), RoutineAdmissionOptions{AgentModelID: "model-pinned"})
			if err != nil {
				t.Fatal(err)
			}
			binding := run.Execution.AgentBindings[0]
			runID := run.Execution.RunID
			replay, err := database.AdmitManualRoutine(ctx, user, id, request, 1, []byte(`{}`), RoutineAdmissionOptions{AgentModelID: "changed-default"})
			if err != nil || replay.Execution.AgentBindings[0].CallNamespace != binding.CallNamespace {
				t.Fatalf("admission replay: %#v %v", replay, err)
			}
			if _, err := database.ActivateAIInvocationRuntime(ctx, runID, "vercel-workflow", runtime); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := database.OpenRoutineAgent(ctx, user, runID, "agent", runtime)
			if err != nil || checkpoint.ModelID != "model-pinned" {
				t.Fatalf("open: %#v %v", checkpoint, err)
			}
			node := "model:routine:" + binding.CallNamespace + ":1"
			if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, "model:1"); err == nil {
				t.Fatal("generic model node escaped routine budget")
			}
			if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, node); err != nil {
				t.Fatal(err)
			}
			if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, node); err != nil {
				t.Fatal(err)
			}
			if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, "model:routine:"+binding.CallNamespace+":2"); err == nil {
				t.Fatal("next turn admitted before usage receipt")
			}
			usage := json.RawMessage(`{"input_tokens":20,"output_tokens":10,"estimated":false}`)
			if err := database.RecordRoutineModelUsage(ctx, user, runID, runtime, node, usage); err != nil {
				t.Fatal(err)
			}
			if err := database.RecordRoutineModelUsage(ctx, user, runID, runtime, node, []byte(`{"changed":true}`)); !errors.Is(err, ErrSpaceConflict) {
				t.Fatalf("usage changed: %v", err)
			}
			call := binding.CallNamespace + ":first"
			tool := binding.Tools[0].ToolName
			if err := database.AdmitRoutineAgentCall(ctx, user, runID, runtime, "agent", call, tool, []byte(`{}`), true); err != nil {
				t.Fatal(err)
			}
			if err := database.AdmitRoutineAgentCall(ctx, user, runID, runtime, "agent", call, tool, []byte(`{"changed":true}`), true); !errors.Is(err, ErrSpaceConflict) {
				t.Fatalf("call input changed: %v", err)
			}
			_, effect := cap.AgentSDKIdentities(user, runID, call)
			journal := AgentToolboxAction{UserID: user, RunID: runID, IdempotencyKey: "sdk-agent-effect:" + effect, RequireSettledRun: true, ToolName: tool, AuditEvent: "routine.fixture", Risk: "write", Request: []byte(`{}`), RedactPayload: true, ProtectResult: func(raw json.RawMessage) ([]byte, error) { return raw, nil }, RestoreResult: func(raw []byte) (json.RawMessage, error) { return raw, nil }}
			executed := 0
			execute := func() (json.RawMessage, error) {
				executed++
				return json.RawMessage(`{"status":"success","result":{},"partial":false,"evidence":[]}`), nil
			}
			if _, err := database.JournalAgentToolboxAction(ctx, journal, execute); err != nil {
				t.Fatal(err)
			}
			expectedCalls, state := 1, "completed"
			if lateCall {
				call = binding.CallNamespace + ":late"
				if err := database.AdmitRoutineAgentCall(ctx, user, runID, runtime, "agent", call, tool, []byte(`{}`), true); err != nil {
					t.Fatal(err)
				}
				expectedCalls = 2
				if _, err := database.FinishRoutineAgent(ctx, user, runID, "agent", runtime, "completed", "fingerprint", make([]byte, 32), 2, 1, false); !errors.Is(err, ErrSpaceConflict) {
					t.Fatalf("missing effect completed: %v", err)
				}
				state = "partial"
			}
			sealed, err := database.FinishRoutineAgent(ctx, user, runID, "agent", runtime, state, "fingerprint", make([]byte, 32), expectedCalls, 1, false)
			if err != nil || sealed.State != state {
				t.Fatalf("finish: %#v %v", sealed, err)
			}
			if _, err := database.FinishRoutineAgent(ctx, user, runID, "agent", runtime, state, "fingerprint", make([]byte, 32), expectedCalls, 1, false); err != nil {
				t.Fatal(err)
			}
			if _, err := database.FinishRoutineAgent(ctx, user, runID, "agent", runtime, state, "changed", make([]byte, 32), expectedCalls, 1, false); !errors.Is(err, ErrSpaceConflict) {
				t.Fatalf("changed checkpoint replay: %v", err)
			}
			if err := database.AdmitRoutineAgentCall(ctx, user, runID, runtime, "agent", binding.CallNamespace+":new", tool, []byte(`{}`), true); err == nil {
				t.Fatal("closed step admitted new call")
			}
			if lateCall {
				_, effect = cap.AgentSDKIdentities(user, runID, call)
				journal.IdempotencyKey = "sdk-agent-effect:" + effect
				if _, err := database.JournalAgentToolboxAction(ctx, journal, execute); !errors.Is(err, ErrSpaceConflict) {
					t.Fatalf("late dispatch: %v", err)
				}
			} else {
				if _, err := database.JournalAgentToolboxAction(ctx, journal, execute); err != nil {
					t.Fatal(err)
				}
				report, _ := json.Marshal(map[string]any{"state": "completed", "steps": []any{map[string]any{"stepId": "agent", "state": "completed", "callId": binding.CallNamespace}}})
				if err := database.PublishRoutineCompletion(ctx, user, runID, "completed", report); err != nil {
					t.Fatal(err)
				}
			}
			if executed != 1 {
				t.Fatalf("effect repeated %d times", executed)
			}
		})
	}
}
