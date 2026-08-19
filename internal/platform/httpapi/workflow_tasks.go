package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) taskQueryNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var config struct {
		Status         string `json:"status"`
		Priority       string `json:"priority"`
		AssigneeUserID string `json:"assigneeUserId"`
		AssignedToMe   bool   `json:"assignedToMe"`
	}
	_ = json.Unmarshal(invocation.Config, &config)
	var input map[string]any
	_ = json.Unmarshal(invocation.Input, &input)
	if value, _ := input["status"].(string); value != "" {
		config.Status = value
	}
	if value, _ := input["assigneeUserId"].(string); value != "" {
		config.AssigneeUserID = value
	}
	if config.AssignedToMe {
		config.AssigneeUserID = run.RequestingMemberID
	}
	if value, _ := input["priority"].(string); value != "" {
		config.Priority = value
	}
	items, err := s.database.SpaceTasks(ctx, run.RequestingMemberID, run.SpaceID, db.SpaceTaskQuery{Status: config.Status, Priority: config.Priority, AssigneeUserID: config.AssigneeUserID, Limit: 200})
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"tasks": items}), nil
}

func (s *SpacesService) calendarQueryNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var config struct {
		DaysBefore int `json:"daysBefore"`
		DaysAfter  int `json:"daysAfter"`
	}
	_ = json.Unmarshal(invocation.Config, &config)
	if config.DaysBefore < 0 || config.DaysBefore > 365 {
		config.DaysBefore = 0
	}
	if config.DaysAfter < 1 || config.DaysAfter > 365 {
		config.DaysAfter = 30
	}
	from := time.Now().UTC().Add(-time.Duration(config.DaysBefore) * 24 * time.Hour)
	to := time.Now().UTC().Add(time.Duration(config.DaysAfter) * 24 * time.Hour)
	var input struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	_ = json.Unmarshal(invocation.Input, &input)
	if parsed, err := time.Parse(time.RFC3339, input.From); err == nil {
		from = parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, input.To); err == nil {
		to = parsed.UTC()
	}
	if !to.After(from) || to.Sub(from) > 370*24*time.Hour {
		return nil, workflowv2.ErrOutputInvalid
	}
	items, err := s.database.SpaceCalendarEvents(ctx, run.RequestingMemberID, run.SpaceID, from, to)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"events": items}), nil
}

func (s *SpacesService) createTaskNode(ctx context.Context, run *db.SpaceRun, agent *db.SpaceStudioResource, invocation workflowv2.Invocation) (json.RawMessage, error) {
	input := workflowTaskInput(invocation.Input)
	if input == nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	item, err := decodeWorkflowTask(input)
	if err != nil {
		return nil, err
	}
	item.SpaceID = run.SpaceID
	item.CreatedByUserID = ""
	item.CreatedByAgentID = agent.ID
	item.SourceRunID = run.ID
	created, err := s.database.CreateSpaceTask(ctx, run.RequestingMemberID, item)
	if err != nil {
		return nil, err
	}
	_, _ = s.ProcessSpaceTaskEvent(ctx, *created, "created")
	return TestingMustAPIRawJSON(map[string]any{"task": created}), nil
}

func (s *SpacesService) updateTaskNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	input := workflowTaskInput(invocation.Input)
	if input == nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	item, err := decodeWorkflowTask(input)
	if err != nil || item.ID == "" || item.Version < 1 {
		return nil, workflowv2.ErrOutputInvalid
	}
	item.SpaceID = run.SpaceID
	updated, err := s.database.UpdateSpaceTask(ctx, run.RequestingMemberID, item)
	if err != nil {
		return nil, err
	}
	_, _ = s.ProcessSpaceTaskEvent(ctx, *updated, "updated")
	return TestingMustAPIRawJSON(map[string]any{"task": updated}), nil
}

func workflowTaskInput(raw json.RawMessage) map[string]any {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if nested, ok := value["task"].(map[string]any); ok {
		return nested
	}
	return value
}

func decodeWorkflowTask(value map[string]any) (db.SpaceTask, error) {
	raw, _ := json.Marshal(value)
	var wire struct {
		ID             string          `json:"id"`
		Title          string          `json:"title"`
		Notes          string          `json:"notes"`
		Status         string          `json:"status"`
		Priority       string          `json:"priority"`
		AssigneeUserID string          `json:"assignee_user_id"`
		AssigneeCamel  string          `json:"assigneeUserId"`
		DueAt          string          `json:"due_at"`
		DueAtCamel     string          `json:"dueAt"`
		DueTimezone    string          `json:"due_timezone"`
		TimezoneCamel  string          `json:"dueTimezone"`
		SourceRefs     json.RawMessage `json:"source_refs"`
		Version        int64           `json:"version"`
	}
	if json.Unmarshal(raw, &wire) != nil || strings.TrimSpace(wire.Title) == "" {
		return db.SpaceTask{}, workflowv2.ErrOutputInvalid
	}
	if wire.AssigneeUserID == "" {
		wire.AssigneeUserID = wire.AssigneeCamel
	}
	if wire.DueAt == "" {
		wire.DueAt = wire.DueAtCamel
	}
	if wire.DueTimezone == "" {
		wire.DueTimezone = wire.TimezoneCamel
	}
	var dueAt *time.Time
	if wire.DueAt != "" {
		value, err := parseAgentToolTime(wire.DueAt, "dueAt", wire.DueTimezone)
		if err != nil {
			return db.SpaceTask{}, workflowv2.ErrOutputInvalid
		}
		dueAt = value
	}
	return db.SpaceTask{ID: wire.ID, Title: wire.Title, Notes: wire.Notes, Status: wire.Status, Priority: wire.Priority, AssigneeUserID: wire.AssigneeUserID, DueAt: dueAt, DueTimezone: wire.DueTimezone, SourceRefs: wire.SourceRefs, Version: wire.Version}, nil
}
