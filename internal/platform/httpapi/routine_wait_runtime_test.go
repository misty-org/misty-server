package api

import (
	"encoding/json"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
)

func TestRoutineWaitEvidenceRequiresCommittedWake(t *testing.T) {
	record, run := routineEvidenceFixture(t)
	var definition map[string]any
	_ = json.Unmarshal(run.Execution.Definition, &definition)
	steps := definition["steps"].([]any)
	steps[0] = map[string]any{"kind": "wait", "id": "read", "label": "Pause", "until": map[string]any{"kind": "literal", "value": "2026-09-08T09:00:00Z"}}
	steps[1].(map[string]any)["input"] = map[string]any{"kind": "reference", "source": map[string]any{"kind": "step", "stepId": "read"}, "path": []any{}}
	run.Execution.Definition, _ = json.Marshal(definition)
	run.Execution.Bindings = run.Execution.Bindings[1:]
	id := cap.RoutineWaitID(record.UserID, record.ID, "read")
	restore := func(string, []byte) (json.RawMessage, error) {
		t.Fatal("timer treated as an external effect")
		return nil, nil
	}
	for _, state := range []string{"pending", "waiting", "completed", "expired", "cancelled"} {
		report, next, err := evaluateRoutineEvidence(record, run, map[string]db.RoutineEffectReceipt{"routine-wait:" + id: {State: state, ToolName: "routine.wait"}}, restore)
		if err != nil {
			t.Fatal(err)
		}
		switch state {
		case "pending", "waiting":
			if next == nil || !next.Wait || next.Binding.CallID != id || report.Steps[1].State != "not_run" {
				t.Fatalf("unconfirmed wake advanced: %+v %+v", report, next)
			}
		case "completed":
			if next == nil || next.Wait || !cap.EqualJSON(next.Input, []byte(`{"resumed":true}`)) {
				t.Fatalf("confirmed wake did not advance: %+v %+v", report, next)
			}
		default:
			if next != nil || report.State != "failed" {
				t.Fatalf("terminal wait advanced: %+v %+v", report, next)
			}
		}
	}
}
