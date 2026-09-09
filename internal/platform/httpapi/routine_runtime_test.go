package api

import (
	"encoding/json"
	"strings"
	"testing"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func routineEvidenceFixture(t *testing.T) (*db.AIInvocationRecord, *db.RoutineRunRecord) {
	t.Helper()
	raw := json.RawMessage(`{"protocol":1,"name":"Habit summary","trigger":{"kind":"manual"},"budget":{},"steps":[{"id":"read","label":"Read","kind":"capability","action":{"capability":"habits.list","capabilityVersion":1,"providerId":"example.habits/backend","providerVersion":1,"targetId":"10000000-0000-4000-8000-000000000001","targetRevision":1},"input":{"kind":"literal","value":{}}},{"id":"save","label":"Save","kind":"capability","action":{"capability":"habits.list","capabilityVersion":1,"providerId":"example.habits/backend","providerVersion":1,"targetId":"10000000-0000-4000-8000-000000000001","targetRevision":1},"input":{"kind":"object","fields":{"summary":{"kind":"reference","source":{"kind":"step","stepId":"read"},"path":["summary"]}}}}]}`)
	if _, _, err := cap.ParseRoutineDefinition(raw); err != nil {
		t.Fatal(err)
	}
	record := &db.AIInvocationRecord{ID: "invocation_fixture", UserID: "user_fixture"}
	run := &db.RoutineRunRecord{Execution: db.RoutineExecution{RunID: record.ID, Definition: raw, Trigger: json.RawMessage(`{}`), Bindings: []db.RoutineCallBinding{{StepID: "read", CallID: "10000000-0000-4000-8000-000000000002", ToolName: "sdk.read"}, {StepID: "save", CallID: "10000000-0000-4000-8000-000000000003", ToolName: "sdk.save"}}}}
	return record, run
}
func TestRoutineEvidenceProgressionAndLostResponse(t *testing.T) {
	record, run := routineEvidenceFixture(t)
	receipts := map[string]db.RoutineEffectReceipt{}
	// Test-only identity restore: production uses authenticated encryption.
	restore := func(_ string, raw []byte) (json.RawMessage, error) { return raw, nil }
	report, next, err := evaluateRoutineEvidence(record, run, receipts, restore)
	if err != nil || report.State != "failed" || next == nil || next.Binding.StepID != "read" || string(next.Input) != "{}" {
		t.Fatalf("initial: %#v %#v %v", report, next, err)
	}
	_, effect := cap.AgentSDKIdentities(record.UserID, record.ID, run.Execution.Bindings[0].CallID)
	receipts["sdk-agent-effect:"+effect] = db.RoutineEffectReceipt{State: "completed", ToolName: "sdk.read", Ciphertext: []byte(`{"status":"success","result":{"summary":"Private habit summary"},"partial":false,"evidence":[]}`)}
	report, next, err = evaluateRoutineEvidence(record, run, receipts, restore)
	if err != nil || report.State != "partial" || next == nil || next.Binding.StepID != "save" || !cap.EqualJSON(next.Input, []byte(`{"summary":"Private habit summary"}`)) {
		t.Fatalf("next confirmed input: %#v %#v %v", report, next, err)
	}
	_, effect = cap.AgentSDKIdentities(record.UserID, record.ID, run.Execution.Bindings[1].CallID)
	receipts["sdk-agent-effect:"+effect] = db.RoutineEffectReceipt{State: "completed", ToolName: "sdk.save", Ciphertext: []byte(`{"status":"success","result":{"saved":true},"partial":false,"evidence":[]}`)}
	report, next, err = evaluateRoutineEvidence(record, run, receipts, restore)
	if err != nil || report.State != "completed" || next != nil {
		t.Fatalf("lost final response: %#v %#v %v", report, next, err)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "Private habit summary") {
		t.Fatal("raw outcome in audit report")
	}
}
func TestRoutineEvidenceStopsAtUncertainFailedOrPartialEffect(t *testing.T) {
	for _, state := range []string{"started", "unknown", "failed", "partial"} {
		t.Run(state, func(t *testing.T) {
			record, run := routineEvidenceFixture(t)
			_, effect := cap.AgentSDKIdentities(record.UserID, record.ID, run.Execution.Bindings[0].CallID)
			receipt := db.RoutineEffectReceipt{State: state, ToolName: "sdk.read"}
			expected := "failed"
			if state == "started" || state == "unknown" {
				expected = "uncertain"
			}
			if state == "partial" {
				expected = "partial"
				receipt.State = "completed"
				receipt.Ciphertext = []byte(`{"status":"success","result":{"summary":"Partial"},"partial":true,"evidence":[]}`)
			}
			report, next, err := evaluateRoutineEvidence(record, run, map[string]db.RoutineEffectReceipt{"sdk-agent-effect:" + effect: receipt}, func(_ string, raw []byte) (json.RawMessage, error) { return raw, nil })
			if err != nil || next != nil || report.State != expected || report.Steps[1].State != "not_run" {
				t.Fatalf("unconfirmed effect: %#v %#v %v", report, next, err)
			}
		})
	}
}
