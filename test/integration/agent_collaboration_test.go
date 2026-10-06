package integration

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func collaborationFixture(t *testing.T) (*db.Database, string, string) {
	t.Helper()
	database := openIntegrationDatabase(t)
	user, err := database.CreateUser("Planner", "collaboration@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.SavePersonalAgent(t.Context(), user.ID, "", db.AgentProfileInput{Name: "Planner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.CreatePersonalAgentConversation(t.Context(), user.ID, "", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return database, user.ID, conversation
}

func questions() []db.AgentQuestion {
	return []db.AgentQuestion{{Header: "Scope", Question: "Which week?", Options: []db.AgentQuestionOption{{Label: "This week"}, {Label: "Last week"}}}}
}

func TestAgentQuestionSetLifecycle(t *testing.T) {
	database, user, conversation := collaborationFixture(t)
	ctx := t.Context()
	run := "invocation_00000000-0000-4000-8000-000000000001"

	set, err := database.OpenAgentQuestionSet(ctx, user, run, "call-1", conversation, questions(), time.Hour)
	if err != nil || set.State != "pending" {
		t.Fatalf("open = %+v, %v", set, err)
	}
	// Replaying the same tool call returns the same set.
	if again, err := database.OpenAgentQuestionSet(ctx, user, run, "call-1", conversation, questions(), time.Hour); err != nil || again.ID != set.ID {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	// Another user cannot see or answer it.
	stranger, _ := database.CreateUser("Stranger", "collaboration-stranger@example.com", "password123")
	if _, err := database.AgentQuestionSet(ctx, stranger.ID, set.ID); !errors.Is(err, db.ErrSpaceNotFound) {
		t.Fatalf("stranger read = %v", err)
	}
	if _, err := database.OpenAgentQuestionSet(ctx, stranger.ID, run, "call-x", conversation, questions(), time.Hour); err == nil {
		t.Fatal("stranger opened questions on someone else's conversation")
	}
	// Invalid answers are rejected; a valid one is accepted exactly once.
	if _, err := database.AnswerAgentQuestionSet(ctx, user, set.ID, []db.AgentQuestionAnswer{{Selected: []string{"Next week"}}}); !errors.Is(err, db.ErrSpaceInvalid) {
		t.Fatalf("invalid answer = %v", err)
	}
	answered, err := database.AnswerAgentQuestionSet(ctx, user, set.ID, []db.AgentQuestionAnswer{{Selected: []string{"This week"}}})
	if err != nil || answered.State != "answered" {
		t.Fatalf("answer = %+v, %v", answered, err)
	}
	if _, err := database.AnswerAgentQuestionSet(ctx, user, set.ID, []db.AgentQuestionAnswer{{Selected: []string{"Last week"}}}); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatalf("second answer = %v", err)
	}
	// The run was still waiting, so handing off returns the answer and no continuation is claimable.
	handed, err := database.HandOffAgentQuestionSet(ctx, user, set.ID)
	if err != nil || handed.State != "answered" || handed.HandedOff {
		t.Fatalf("hand-off after answer = %+v, %v", handed, err)
	}
	if claimed, _ := database.ClaimAgentQuestionContinuation(ctx, user, set.ID); claimed {
		t.Fatal("an answer the run received also continued the conversation")
	}

	// A set that handed off first continues the conversation exactly once.
	late, _ := database.OpenAgentQuestionSet(ctx, user, run, "call-2", conversation, questions(), time.Hour)
	if handed, err := database.HandOffAgentQuestionSet(ctx, user, late.ID); err != nil || handed.State != "pending" || !handed.HandedOff {
		t.Fatalf("hand-off = %+v, %v", handed, err)
	}
	if _, err := database.AnswerAgentQuestionSet(ctx, user, late.ID, []db.AgentQuestionAnswer{{Other: "Both weeks"}}); err != nil {
		t.Fatal(err)
	}
	if claimed, _ := database.ClaimAgentQuestionContinuation(ctx, user, late.ID); !claimed {
		t.Fatal("handed-off answer did not continue")
	}
	if claimed, _ := database.ClaimAgentQuestionContinuation(ctx, user, late.ID); claimed {
		t.Fatal("handed-off answer continued twice")
	}

	// A new call supersedes the run's open set; a new message supersedes the conversation's.
	first, _ := database.OpenAgentQuestionSet(ctx, user, run, "call-3", conversation, questions(), time.Hour)
	second, _ := database.OpenAgentQuestionSet(ctx, user, run, "call-4", conversation, questions(), time.Hour)
	if current, _ := database.AgentQuestionSet(ctx, user, first.ID); current.State != "superseded" {
		t.Fatalf("earlier set = %s", current.State)
	}
	if err := database.SetAgentQuestionState(ctx, user, conversation, "", "superseded"); err != nil {
		t.Fatal(err)
	}
	if current, _ := database.AgentQuestionSet(ctx, user, second.ID); current.State != "superseded" {
		t.Fatalf("superseded by message = %s", current.State)
	}
	if err := database.SetAgentQuestionState(ctx, user, "", "", "superseded"); err == nil {
		t.Fatal("an unscoped supersede was accepted")
	}
}

func TestAgentPlanVersionsApprovalAndProgress(t *testing.T) {
	database, user, conversation := collaborationFixture(t)
	ctx := t.Context()
	payload := db.AgentPlanPayload{Title: "Weekly review", Summary: "Read, then write.", Steps: []db.AgentPlanStep{{ID: "read", Title: "Read notes"}, {ID: "write", Title: "Write the review", Risk: "write"}}, SuccessCriteria: []string{"Review Note exists"}}

	first, err := database.ProposeAgentPlan(ctx, user, conversation, "invocation_a", "agent", payload)
	if err != nil || first.Version != 1 || first.State != "proposed" {
		t.Fatalf("propose = %+v, %v", first, err)
	}
	if replay, _ := database.ProposeAgentPlan(ctx, user, conversation, "invocation_a", "agent", payload); replay.ID != first.ID {
		t.Fatal("a replayed proposal made a new version")
	}
	payload.Steps = append(payload.Steps, db.AgentPlanStep{ID: "share", Title: "Share it", Risk: "consequential"})
	second, err := database.ProposeAgentPlan(ctx, user, conversation, "", "user", payload)
	if err != nil || second.Version != 2 || second.Author != "user" {
		t.Fatalf("revise = %+v, %v", second, err)
	}
	if stale, _ := database.AgentPlanByID(ctx, user, first.ID); stale.State != "superseded" {
		t.Fatalf("earlier version = %s", stale.State)
	}
	if _, err := database.ApproveAgentPlan(ctx, user, second.ID, 1); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatalf("approving a version the user did not review = %v", err)
	}
	if _, err := database.UpdateAgentPlanProgress(ctx, user, conversation, []db.AgentPlanProgressUpdate{{ID: "read", Status: "done"}}); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatalf("progress before approval = %v", err)
	}
	if _, err := database.ApproveAgentPlan(ctx, user, second.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateAgentPlanProgress(ctx, user, conversation, []db.AgentPlanProgressUpdate{{ID: "invented", Status: "done"}}); !errors.Is(err, db.ErrSpaceInvalid) {
		t.Fatalf("unknown step = %v", err)
	}
	progressed, err := database.UpdateAgentPlanProgress(ctx, user, conversation, []db.AgentPlanProgressUpdate{{ID: "read", Status: "done"}, {ID: "write", Status: "done"}})
	if err != nil || progressed.State != "approved" {
		t.Fatalf("partial progress = %+v, %v", progressed, err)
	}
	done, err := database.UpdateAgentPlanProgress(ctx, user, conversation, []db.AgentPlanProgressUpdate{{ID: "share", Status: "skipped", Note: "Nobody to share with"}})
	if err != nil || done.State != "completed" {
		t.Fatalf("completion = %+v, %v", done, err)
	}
	if history, _ := database.AgentPlanHistory(ctx, user, conversation, 10); len(history) != 2 || history[0].Version != 2 {
		t.Fatalf("history = %+v", history)
	}
}

func TestAgentGoalBudgetAndContinuations(t *testing.T) {
	database, user, conversation := collaborationFixture(t)
	ctx := t.Context()
	goal, err := database.SetAgentGoal(ctx, user, conversation, "Inbox zero", []string{"Inbox is empty"}, 100_000)
	if err != nil || goal.Status != "pursuing" {
		t.Fatalf("set = %+v, %v", goal, err)
	}
	// A run is counted once; a continuation is claimed once per finished run.
	if counted, _ := database.RecordAgentGoalRun(ctx, user, conversation, "invocation_1", 40_000); counted.UsedTokens != 40_000 {
		t.Fatalf("used = %d", counted.UsedTokens)
	}
	if counted, _ := database.RecordAgentGoalRun(ctx, user, conversation, "invocation_1", 40_000); counted.UsedTokens != 40_000 {
		t.Fatalf("a replayed completion counted twice: %d", counted.UsedTokens)
	}
	if claimed, _ := database.ClaimAgentGoalContinuation(ctx, user, goal.ID, "invocation_1"); !claimed {
		t.Fatal("first claim failed")
	}
	if claimed, _ := database.ClaimAgentGoalContinuation(ctx, user, goal.ID, "invocation_1"); claimed {
		t.Fatal("the same finished run continued twice")
	}
	// Spending the budget stops the goal; resuming needs a larger budget.
	limited, _ := database.RecordAgentGoalRun(ctx, user, conversation, "invocation_2", 70_000)
	if limited.Status != "budget_limited" {
		t.Fatalf("status = %s", limited.Status)
	}
	if _, err := database.ControlAgentGoal(ctx, user, goal.ID, "pursuing", 0); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatalf("resume without budget = %v", err)
	}
	resumed, err := database.ControlAgentGoal(ctx, user, goal.ID, "pursuing", 200_000)
	if err != nil || resumed.Status != "pursuing" || resumed.ContinuationCount != 0 {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	report, _ := json.Marshal(map[string]any{"status": "achieved", "summary": "Inbox is empty."})
	achieved, err := database.ReportAgentGoal(ctx, user, conversation, "achieved", report)
	if err != nil || achieved.Status != "achieved" {
		t.Fatalf("report = %+v, %v", achieved, err)
	}
	if _, err := database.ReportAgentGoal(ctx, user, conversation, "pursuing", report); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatalf("report on a finished goal = %v", err)
	}
	// A new goal replaces an active one; at most one is ever active.
	next, _ := database.SetAgentGoal(ctx, user, conversation, "Plan next week", nil, 0)
	paused, err := database.ControlAgentGoal(ctx, user, next.ID, "paused", 0)
	if err != nil || paused.Status != "paused" || paused.BudgetTokens != db.DefaultGoalBudgetTokens {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	if current, _ := database.CurrentAgentGoal(ctx, user, conversation); current.ID != next.ID {
		t.Fatal("current goal is not the newest")
	}
	// Mode and every collaboration row go with a deleted conversation.
	if err := database.SetConversationMode(ctx, user, conversation, db.ConversationModePlan); err != nil {
		t.Fatal(err)
	}
	if mode, _ := database.ConversationMode(ctx, user, conversation); mode != db.ConversationModePlan {
		t.Fatalf("mode = %s", mode)
	}
	if err := database.DeleteAgentConversation(ctx, user, conversation); err != nil {
		t.Fatal(err)
	}
	if current, _ := database.CurrentAgentGoal(ctx, user, conversation); current != nil {
		t.Fatal("goal outlived its conversation")
	}
}
