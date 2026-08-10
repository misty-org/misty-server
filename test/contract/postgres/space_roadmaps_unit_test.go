package db

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceRoadmapProgressDerivation(t *testing.T) {
	now := time.Now()
	archived := now.Add(-time.Hour)
	snapshot := SpaceRoadmapSnapshot{
		Milestones: []SpaceRoadmapMilestone{{ID: "milestone-a"}, {ID: "milestone-b"}},
		Goals: []SpaceRoadmapGoal{
			{
				ID: "goal-tasks", MilestoneID: "milestone-a",
				Tasks: []SpaceTask{
					{ID: "done", Status: "done"},
					{ID: "working", Status: "in_progress"},
					{ID: "canceled", Status: "canceled"},
					{ID: "archived", Status: "done", ArchivedAt: &archived},
				},
			},
			{ID: "goal-manual", MilestoneID: "milestone-a", ManualCompletedAt: &now},
			{ID: "goal-empty", MilestoneID: "milestone-b"},
		},
	}

	TestingCalculateSpaceRoadmapProgress(&snapshot)

	if got := snapshot.Goals[0]; got.Status != "in_progress" || got.TaskTotal != 2 || got.TaskDone != 1 || got.ProgressPercentage != 50 {
		t.Fatalf("task goal = %#v", got)
	}
	if got := snapshot.Goals[1]; got.Status != "done" || got.ProgressPercentage != 100 {
		t.Fatalf("manual goal = %#v", got)
	}
	if snapshot.GoalTotal != 3 || snapshot.GoalDone != 1 || snapshot.ProgressPercentage != 33 {
		t.Fatalf("roadmap totals = %d/%d (%d%%)", snapshot.GoalDone, snapshot.GoalTotal, snapshot.ProgressPercentage)
	}
	if got := snapshot.Milestones[0]; got.Status != "in_progress" || got.GoalTotal != 2 || got.GoalDone != 1 {
		t.Fatalf("first milestone = %#v", got)
	}
	if got := snapshot.Milestones[1]; got.Status != "not_started" {
		t.Fatalf("second milestone status = %q", got.Status)
	}
}

func TestRoadmapDependencyCycleDetection(t *testing.T) {
	edges := []SpaceRoadmapEdge{
		{SourceGoalID: "a", TargetGoalID: "b", EdgeType: "dependency"},
		{SourceGoalID: "b", TargetGoalID: "c", EdgeType: "dependency"},
		{SourceGoalID: "c", TargetGoalID: "a", EdgeType: "related"},
	}
	if !TestingRoadmapDependencyWouldCycle(edges, "c", "a") {
		t.Fatal("expected c -> a dependency to close the dependency cycle")
	}
	if TestingRoadmapDependencyWouldCycle(edges, "c", "d") {
		t.Fatal("unconnected dependency must not be rejected")
	}
	if !TestingRoadmapDependencyWouldCycle(edges, "a", "a") {
		t.Fatal("self dependency must be rejected")
	}
}

func TestRoadmapCausalEdgesShareCycleDetection(t *testing.T) {
	edges := []SpaceRoadmapEdge{
		{Source: SpaceRoadmapEdgeEndpoint{Kind: "goal", ID: "a"}, Target: SpaceRoadmapEdgeEndpoint{Kind: "goal", ID: "b"}, EdgeType: "enables"},
		{Source: SpaceRoadmapEdgeEndpoint{Kind: "goal", ID: "b"}, Target: SpaceRoadmapEdgeEndpoint{Kind: "goal", ID: "c"}, EdgeType: "blocks"},
	}
	if !TestingRoadmapDependencyWouldCycle(edges, "c", "a") {
		t.Fatal("all causal goal edges must participate in cycle detection")
	}
	if TestingRoadmapDependencyWouldCycle(edges, "c", "d") {
		t.Fatal("an unrelated causal edge must remain valid")
	}
}

func TestRoadmapCustomFieldSchemaValidation(t *testing.T) {
	valid := json.RawMessage(`[
		{"id":"owner_note","label":"Owner note","type":"short_text"},
		{"id":"confidence","label":"Confidence","type":"select","options":["Low","High"]},
		{"id":"approved","label":"Approved","type":"checkbox"}
	]`)
	fields, ok := TestingDecodeRoadmapFieldSchema(valid)
	if !ok || len(fields) != 3 {
		t.Fatalf("valid schema rejected: %#v", fields)
	}
	if !TestingValidateRoadmapFieldValues(json.RawMessage(`{"owner_note":"Misty","confidence":"High","approved":true}`), fields) {
		t.Fatal("valid typed values rejected")
	}
	if TestingValidateRoadmapFieldValues(json.RawMessage(`{"confidence":"Unknown"}`), fields) {
		t.Fatal("unknown select option accepted")
	}
	if TestingValidateRoadmapFieldValues(json.RawMessage(`{"approved":"yes"}`), fields) {
		t.Fatal("invalid checkbox value accepted")
	}
	if _, ok := TestingDecodeRoadmapFieldSchema(json.RawMessage(`[{"id":"same","label":"One","type":"date"},{"id":"same","label":"Two","type":"date"}]`)); ok {
		t.Fatal("duplicate field ids accepted")
	}
	if TestingValidRoadmapDefinitionUpdate(
		[]SpaceRoadmapFieldDefinition{{ID: "score", Label: "Score", Type: "number"}},
		[]SpaceRoadmapFieldDefinition{{ID: "score", Label: "Score", Type: "short_text"}},
	) {
		t.Fatal("field type mutation accepted")
	}
	if TestingValidRoadmapDefinitionUpdate(
		[]SpaceRoadmapFieldDefinition{{ID: "score", Label: "Score", Type: "number"}},
		[]SpaceRoadmapFieldDefinition{},
	) {
		t.Fatal("field deletion accepted instead of archival")
	}
}
