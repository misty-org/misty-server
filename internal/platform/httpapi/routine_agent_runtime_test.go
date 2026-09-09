package api

import (
	"encoding/json"
	"testing"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestRoutineAgentOutputRequiresJournalAndModelEvidence(t *testing.T) {
	restore := func(_ string, b []byte) (json.RawMessage, error) { return b, nil }
	step := cap.RoutineStep{OutputSchema: json.RawMessage(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string"}},"additionalProperties":false}`)}
	checkpoint := db.RoutineAgentCheckpoint{CallNamespace: "10000000-0000-4000-8000-000000000001", MaxTurns: 2}
	complete := routineAgentResult{State: "completed", Output: json.RawMessage(`{"summary":"private result"}`), ModelTurns: 2, CapabilityCalls: 1}
	receipt := db.RoutineAgentCallReceipt{State: "completed", EffectID: checkpoint.CallNamespace, Ciphertext: []byte(`{"status":"success","result":{},"partial":false,"evidence":[]}`)}
	for _, test := range []struct {
		name, state string
		receipts    []db.RoutineAgentCallReceipt
		result      routineAgentResult
		finished    int
		recovery    bool
	}{
		{"confirmed", "completed", []db.RoutineAgentCallReceipt{receipt}, complete, 2, false},
		{"missing effect", "failed", nil, complete, 2, false},
		{"lost write response", "uncertain", []db.RoutineAgentCallReceipt{{State: "started", EffectID: receipt.EffectID}}, complete, 2, false},
		{"no model receipt", "partial", []db.RoutineAgentCallReceipt{receipt}, complete, 1, false},
		{"schema mismatch", "partial", []db.RoutineAgentCallReceipt{receipt}, routineAgentResult{State: "completed", Output: json.RawMessage(`{"invented":true}`), ModelTurns: 2, CapabilityCalls: 1}, 2, false},
		{"worker stopped after effect", "partial", []db.RoutineAgentCallReceipt{receipt}, routineAgentResult{State: "failed"}, 2, true},
		{"worker stopped before effect", "failed", nil, routineAgentResult{State: "failed"}, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, outcome, err := evaluateRoutineAgentResult(step, checkpoint, test.result, test.receipts, restore, 2, test.finished, test.recovery)
			if err != nil || state != test.state {
				t.Fatalf("state=%s outcome=%s err=%v", state, outcome, err)
			}
			if state != "completed" && json.Valid(outcome) {
				var raw map[string]any
				_ = json.Unmarshal(outcome, &raw)
				if raw["result"] != nil {
					t.Fatal("unconfirmed model output escaped checkpoint")
				}
			}
		})
	}
}
func TestRoutineAgentCheckpointFeedsOnlyConfirmedOutputForward(t *testing.T) {
	record, run := routineEvidenceFixture(t)
	var definition map[string]any
	_ = json.Unmarshal(run.Execution.Definition, &definition)
	steps := definition["steps"].([]any)
	action := steps[0].(map[string]any)["action"]
	steps[0] = map[string]any{"kind": "agent", "id": "read", "label": "Summarize", "prompt": map[string]any{"kind": "literal", "value": "Read habits"}, "actions": []any{action}, "maxTurns": 2, "outputSchema": map[string]any{"type": "object"}}
	run.Execution.Definition, _ = json.Marshal(definition)
	namespace := run.Execution.Bindings[0].CallID
	run.Execution.Bindings = run.Execution.Bindings[1:]
	run.Execution.AgentBindings = []db.RoutineAgentBinding{{StepID: "read", CallNamespace: namespace}}
	key := "routine-agent:" + namespace
	restore := func(aad string, b []byte) (json.RawMessage, error) {
		if aad != key+":outcome" {
			t.Fatalf("wrong checkpoint encryption context: %s", aad)
		}
		return b, nil
	}
	receipts := map[string]db.RoutineEffectReceipt{key: {ToolName: "routine.agent", State: "running"}}
	report, next, err := evaluateRoutineEvidence(record, run, receipts, restore)
	if err != nil || next == nil || next.Agent == nil || string(next.Input) != `"Read habits"` || report.Steps[1].State != "not_run" {
		t.Fatalf("open: %+v %+v %v", report, next, err)
	}
	receipts[key] = db.RoutineEffectReceipt{ToolName: "routine.agent", State: "completed", Ciphertext: []byte(`{"status":"success","result":{"summary":"Confirmed summary"},"partial":false,"evidence":[]}`)}
	plan, err := evaluateRoutinePlan(record, run, receipts, restore)
	if err != nil || plan.Next == nil || plan.Next.Binding.StepID != "save" || !cap.EqualJSON(plan.Next.Input, []byte(`{"summary":"Confirmed summary"}`)) || plan.Replays[namespace].Agent == nil {
		t.Fatalf("advance: %+v %v", plan, err)
	}
	for _, state := range []string{"failed", "partial", "uncertain"} {
		receipts[key] = db.RoutineEffectReceipt{ToolName: "routine.agent", State: state}
		report, next, err = evaluateRoutineEvidence(record, run, receipts, restore)
		if err != nil || next != nil || report.State != state || report.Steps[1].State != "not_run" {
			t.Fatalf("halt %s: %+v %+v %v", state, report, next, err)
		}
	}
}
