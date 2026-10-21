package api

import (
	"context"
	"encoding/json"
	"errors"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// collaborationChecklist publishes the run's live checklist. plan.update is an
// alias that also records progress on the approved plan.
const collaborationChecklist = "log.work.checklist"

// checklistToolRegistrations offers the checklist to every turn outside Plan,
// including delegated runs, whose checklist nests under their parent's.
func (s *SpacesService) checklistToolRegistrations(record *db.AIInvocationRecord, state collaborationState) []agenttools.Registration {
	if record == nil || state.planning() {
		return nil
	}
	var body aiInvocationInput
	_ = json.Unmarshal(record.RequestPayload, &body)
	if record.ConversationID == "" && body.DelegatedFrom == "" {
		return nil
	}
	return []agenttools.Registration{{Descriptor: collaborationDescriptor(collaborationChecklist,
		"Publish your live checklist for multi-step work: 3-8 concrete steps before you start, then update it as you go. Exactly one step is in_progress at a time; mark it done before starting the next, and skipped or blocked with a short note. Mark steps you hand to another agent or a coding tool as delegated. Send only changed steps; replace restates the whole list.",
		map[string]any{
			"items": map[string]any{"type": "array", "minItems": 1, "maxItems": db.MaxChecklistItems, "items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"id", "status"},
				"properties": map[string]any{
					"id":        map[string]any{"type": "string", "minLength": 1, "maxLength": 40, "description": "Stable id, such as s1."},
					"title":     map[string]any{"type": "string", "maxLength": 160, "description": "Imperative; required for a new step."},
					"status":    map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done", "skipped", "blocked"}},
					"note":      map[string]any{"type": "string", "maxLength": 500},
					"delegated": map[string]any{"type": "boolean"},
				},
			}},
			"replace": map[string]any{"type": "boolean", "description": "True to replace the whole checklist."},
			// The run's time budget follows it (agent-runtime src/time-budget.ts).
			"minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 240, "description": "With your first checklist: how long the whole task should take you, in minutes, as the person would expect it from a quick assistant. Most tasks are 3-15: a quick lookup 2-3, a few pages on one site 5, comparing several sites 8-12, a multi-site plan with tabs kept open 15. Say 30 or more only when the person asked for in-depth research or a long written deliverable. Your time budget follows it; be honest, not generous."},
		}, []string{"items"}), Handler: s.checklistHandler(record, state)}}
}

func (s *SpacesService) checklistHandler(record *db.AIInvocationRecord, state collaborationState) agenttools.Handler {
	return func(ctx context.Context, _ agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
		var input struct {
			Items   []db.ChecklistItem `json:"items"`
			Replace bool               `json:"replace"`
			Minutes int                `json:"minutes"`
		}
		if json.Unmarshal(request.Arguments, &input) != nil {
			return nil, agenttools.ErrArgumentsInvalid
		}
		checklist, err := s.publishChecklist(ctx, record, state, input.Items, input.Replace)
		if errors.Is(err, db.ErrSpaceInvalid) {
			return nil, serveragent.ErrInvalidRequest("Each step needs an id, a title when new, and one of pending, in_progress, done, skipped or blocked; at most 20 steps.")
		}
		if err != nil {
			return nil, err
		}
		result, err := checklistResult(checklist)
		if err != nil || input.Minutes <= 0 {
			return result, err
		}
		// The estimate goes back with the list: the run's time budget and the evals read it.
		var reply map[string]any
		if json.Unmarshal(result, &reply) != nil {
			return result, nil
		}
		reply["estimateMinutes"] = input.Minutes
		return json.Marshal(reply)
	}
}

// publishChecklist saves the run's checklist and shows it live: in the run's
// own stream, and in its parent's when the run was delegated.
func (s *SpacesService) publishChecklist(ctx context.Context, record *db.AIInvocationRecord, state collaborationState, items []db.ChecklistItem, replace bool) (*db.AgentChecklist, error) {
	var body aiInvocationInput
	_ = json.Unmarshal(record.RequestPayload, &body)
	conversation := record.ConversationID
	var parent *db.AIInvocationRecord
	if body.DelegatedFrom != "" {
		if found, err := s.database.AIInvocationByID(ctx, record.UserID, body.DelegatedFrom); err == nil {
			parent = found
			if conversation == "" {
				conversation = found.ConversationID
			}
		}
	}
	if conversation == "" {
		return nil, serveragent.ErrInvalidRequest("This run has no conversation to show a checklist in.")
	}
	checklist, err := s.database.SaveAgentChecklist(ctx, record.UserID, record.ID, conversation, body.DelegatedFrom, planChecklistSeed(state.approvedPlan()), items, replace)
	if err != nil {
		return nil, err
	}
	event := aiInvocationEvent{Type: "checklist.updated", aiCollaborationEventFields: aiCollaborationEventFields{Checklist: checklist}}
	s.showCollaborationEvent(ctx, record, event)
	if parent != nil {
		s.showCollaborationEvent(ctx, parent, event)
	}
	return checklist, nil
}

// planChecklistSeed is an approved plan's steps with their recorded progress,
// the starting checklist for a run that follows the plan.
func planChecklistSeed(plan *db.AgentPlan) []db.ChecklistItem {
	if plan == nil {
		return nil
	}
	seed := make([]db.ChecklistItem, 0, len(plan.Payload.Steps))
	for _, step := range plan.Payload.Steps {
		status := plan.Progress[step.ID].Status
		if status == "" {
			status = "pending"
		}
		seed = append(seed, db.ChecklistItem{ID: step.ID, Title: step.Title, Status: status, Note: plan.Progress[step.ID].Note})
	}
	return seed
}

func checklistResult(checklist *db.AgentChecklist) (json.RawMessage, error) {
	remaining := []string{}
	for _, item := range checklist.Items {
		if item.Status != "done" && item.Status != "skipped" {
			remaining = append(remaining, item.ID)
		}
	}
	return json.Marshal(map[string]any{"status": "recorded", "version": checklist.Version, "remaining": remaining})
}
