package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func (s *SpacesService) proposePlanHandler(record *db.AIInvocationRecord) agenttools.Handler {
	return func(ctx context.Context, _ agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
		var input db.AgentPlanPayload
		if json.Unmarshal(request.Arguments, &input) != nil {
			return nil, agenttools.ErrArgumentsInvalid
		}
		plan, err := s.database.ProposeAgentPlan(ctx, record.UserID, record.ConversationID, record.ID, "agent", input)
		if errors.Is(err, db.ErrSpaceInvalid) {
			return nil, agent.ErrInvalidRequest("A plan needs a title, a summary of at most 1,200 characters, and 1-12 steps, each with a unique id, an imperative title and a risk of read, write, draft, consequential or dangerous.")
		}
		if err != nil {
			return nil, err
		}
		s.showCollaborationEvent(ctx, record, aiInvocationEvent{Type: "plan.proposed", aiCollaborationEventFields: aiCollaborationEventFields{Plan: plan}})
		return MustAPIRawJSON(map[string]any{
			"status": "plan_proposed", "plan": plan.ID, "version": plan.Version,
			"message":      "The plan is showing to the user. This run ends here; the user runs it, edits it or asks you to keep planning.",
			"user_message": "Review the plan above. Run it, edit it, or keep planning.",
		}), nil
	}
}

func (s *SpacesService) updatePlanHandler(record *db.AIInvocationRecord, state collaborationState) agenttools.Handler {
	return func(ctx context.Context, _ agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
		var input struct {
			Steps []db.AgentPlanProgressUpdate `json:"steps"`
		}
		if json.Unmarshal(request.Arguments, &input) != nil {
			return nil, agenttools.ErrArgumentsInvalid
		}
		plan, err := s.database.UpdateAgentPlanProgress(ctx, record.UserID, record.ConversationID, input.Steps)
		if errors.Is(err, db.ErrSpaceInvalid) {
			return nil, agent.ErrInvalidRequest("Use the approved plan's step ids and one of pending, in_progress, done, skipped or blocked.")
		}
		if errors.Is(err, db.ErrSpaceConflict) {
			return nil, agent.ErrInvalidRequest("This conversation has no approved plan in progress.")
		}
		if err != nil {
			return nil, err
		}
		s.showCollaborationEvent(ctx, record, aiInvocationEvent{Type: "plan.updated", aiCollaborationEventFields: aiCollaborationEventFields{Plan: plan}})
		// plan.update is an alias of the live checklist, seeded from the plan.
		items := make([]db.ChecklistItem, 0, len(input.Steps))
		for _, step := range input.Steps {
			items = append(items, db.ChecklistItem{ID: step.ID, Status: step.Status, Note: step.Note})
		}
		state.plan = plan
		_, _ = s.publishChecklist(ctx, record, state, items, false)
		remaining := []string{}
		for _, step := range plan.Payload.Steps {
			if status := plan.Progress[step.ID].Status; status != "done" && status != "skipped" {
				remaining = append(remaining, step.ID)
			}
		}
		return json.Marshal(map[string]any{"status": "recorded", "plan_state": plan.State, "remaining": remaining})
	}
}

// goalEvidenceComplete is true when every success criterion has evidence. A
// goal without criteria needs at least one piece of evidence.
func goalEvidenceComplete(criteria []string, evidence []struct {
	Criterion string `json:"criterion"`
	Proof     string `json:"proof"`
}) bool {
	normalize := func(value string) string { return strings.ToLower(strings.Join(strings.Fields(value), " ")) }
	proven := map[string]bool{}
	for _, item := range evidence {
		if strings.TrimSpace(item.Proof) != "" {
			proven[normalize(item.Criterion)] = true
		}
	}
	if len(criteria) == 0 {
		return len(proven) > 0
	}
	for _, criterion := range criteria {
		if !proven[normalize(criterion)] {
			return false
		}
	}
	return true
}

func (s *SpacesService) updateGoalHandler(record *db.AIInvocationRecord) agenttools.Handler {
	return func(ctx context.Context, _ agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
		var input struct {
			Status   string `json:"status"`
			Summary  string `json:"summary"`
			Evidence []struct {
				Criterion string `json:"criterion"`
				Proof     string `json:"proof"`
			} `json:"evidence"`
			NextSteps []string `json:"next_steps"`
		}
		if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Summary) == "" {
			return nil, agenttools.ErrArgumentsInvalid
		}
		goal, err := s.database.CurrentAgentGoal(ctx, record.UserID, record.ConversationID)
		if err != nil {
			return nil, err
		}
		if goal == nil || goal.Status != "pursuing" {
			return nil, agent.ErrInvalidRequest("This conversation has no goal being pursued.")
		}
		status := map[string]string{"in_progress": "pursuing", "achieved": "achieved", "unmet": "unmet"}[input.Status]
		if status == "" {
			return nil, agenttools.ErrArgumentsInvalid
		}
		if status == "achieved" && !goalEvidenceComplete(goal.SuccessCriteria, input.Evidence) {
			return nil, agent.ErrInvalidRequest("Achieved needs inspected evidence for every success criterion, quoting each one exactly. Inspect what is missing, or report in_progress.")
		}
		report, _ := json.Marshal(map[string]any{"status": input.Status, "summary": truncateAgentRuntimeText(strings.TrimSpace(input.Summary), 2000),
			"evidence": input.Evidence, "next_steps": input.NextSteps, "invocation_id": record.ID, "reported_at": time.Now().UTC()})
		updated, err := s.database.ReportAgentGoal(ctx, record.UserID, record.ConversationID, status, report)
		if err != nil {
			return nil, err
		}
		s.showCollaborationEvent(ctx, record, aiInvocationEvent{Type: "goal.updated", aiCollaborationEventFields: aiCollaborationEventFields{Goal: updated}})
		return json.Marshal(map[string]any{"status": "recorded", "goal_status": updated.Status})
	}
}
