package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// A schedule belongs to a workflow, like the Workflows page. methods.list shows
// each workflow's schedule; to schedule a plain request, save it as a workflow
// with methods.save first. Removing a schedule asks first when the account asks first.
func (s *SpacesService) scheduleToolRegistrations() []agenttools.Registration {
	base := agenttools.Descriptor{Version: 1, OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, Approval: agenttools.ApprovalNone}
	ints := func(low, high int, description string) map[string]any {
		return map[string]any{"type": "array", "maxItems": 35, "description": description, "items": map[string]any{"type": "integer", "minimum": low, "maximum": high}}
	}
	clock := map[string]any{"type": "string", "pattern": "^[0-2][0-9]:[0-5][0-9]$"}
	date := map[string]any{"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}
	rule := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"frequency"},
		"properties": map[string]any{
			"frequency":  map[string]any{"type": "string", "enum": []string{"once", "hourly", "daily", "weekly", "monthly", "yearly"}},
			"interval":   map[string]any{"type": "integer", "minimum": 1, "maximum": 365, "description": "Every N hours, days, weeks, months or years. Defaults to 1."},
			"dates":      map[string]any{"type": "array", "maxItems": 100, "items": date, "description": "once: the dates, as YYYY-MM-DD."},
			"times":      map[string]any{"type": "array", "maxItems": 24, "items": clock, "description": "24-hour local times, such as 08:30. Required for every frequency except hourly."},
			"minute":     map[string]any{"type": "integer", "minimum": 0, "maximum": 59, "description": "hourly: the minute past each hour."},
			"from":       clock,
			"until":      clock,
			"weekdays":   ints(0, 6, "0 is Sunday. Required for weekly; narrows hourly and daily."),
			"month_days": ints(-1, 31, "monthly and yearly: days of the month; -1 is the last day."),
			"nth_weekdays": map[string]any{"type": "array", "maxItems": 35, "description": "monthly and yearly: such as the second Tuesday (nth 2, weekday 2) or the last Friday (nth -1).", "items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"nth", "weekday"},
				"properties": map[string]any{"nth": map[string]any{"type": "integer", "minimum": -1, "maximum": 5}, "weekday": map[string]any{"type": "integer", "minimum": 0, "maximum": 6}},
			}},
			"months":     ints(1, 12, "yearly: the months."),
			"start_date": date,
			"end_date":   date,
		},
	}
	workflowID := map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "The workflow's id from methods_list."}
	set, remove := base, base
	set.Name, set.Risk, set.AuditEvent, set.Idempotent = "workflows.schedule", serveragent.RiskWrite, "workflow.schedule.saved", true
	set.Description = "Set when a workflow runs on its own, replacing any schedule it had. A schedule is one or more rules in one timezone; a run happens at every moment any rule produces. Each run uses the workflow's latest version. Pause with enabled false."
	set.InputSchema = agentToolSchema(map[string]any{
		"workflow_id": workflowID,
		"timezone":    map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "IANA timezone, such as America/Los_Angeles."},
		"rules":       map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": rule},
		"skip_dates":  map[string]any{"type": "array", "maxItems": 366, "items": date, "description": "Dates with no runs."},
		"inputs":      map[string]any{"type": "object", "description": "Values for the workflow's inputs on every scheduled run."},
		"enabled":     map[string]any{"type": "boolean"},
	}, []string{"workflow_id", "timezone", "rules"})
	remove.Name, remove.Risk, remove.AuditEvent = "workflows.unschedule", serveragent.RiskWrite, "workflow.schedule.deleted"
	remove.Description = "Remove a workflow's schedule so it runs only when started. Use only when the user asked; to stop it for now, set enabled false with workflows_schedule instead."
	remove.InputSchema = agentToolSchema(map[string]any{"workflow_id": workflowID}, []string{"workflow_id"})
	return []agenttools.Registration{
		{Descriptor: set, Handler: s.executeWorkflowSchedule},
		{Descriptor: remove, Handler: s.executeWorkflowUnschedule},
	}
}

const invalidScheduleMessage = "give a workflow_id from methods_list, an IANA timezone and rules: times as HH:MM for every frequency except hourly, dates for once, weekdays for weekly, month_days or nth_weekdays for monthly and yearly, months for yearly; and a value for every required workflow input"

func (s *SpacesService) executeWorkflowSchedule(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		WorkflowID string `json:"workflow_id"`
		workflowScheduleInput
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	saved, err := saveWorkflowSchedule(ctx, s.database, invocation.UserID, strings.TrimSpace(input.WorkflowID), input.workflowScheduleInput, time.Now().UTC())
	switch {
	case err == db.ErrSpaceInvalid || err == db.ErrSpaceNotFound:
		return nil, serveragent.ErrInvalidRequest(invalidScheduleMessage)
	case err == db.ErrSpaceConflict:
		return nil, serveragent.ErrInvalidRequest("this account already has the limit of 50 scheduled workflows")
	case err != nil:
		return nil, err
	}
	return json.Marshal(map[string]any{"schedule": saved})
}

func (s *SpacesService) executeWorkflowUnschedule(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		WorkflowID string `json:"workflow_id"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	id := strings.TrimSpace(input.WorkflowID)
	method, err := s.database.AgentMethodByID(ctx, invocation.UserID, id)
	if err != nil {
		return nil, serveragent.ErrInvalidRequest("no workflow " + id + "; call methods_list for ids")
	}
	if _, err := s.database.WorkflowScheduleByMethod(ctx, invocation.UserID, id); err != nil {
		return nil, serveragent.ErrInvalidRequest("workflow " + id + " has no schedule")
	}
	approval, waiting, err := s.confirmAgentAction(ctx, invocation, "misty.workflows.unschedule", id, "Remove the schedule for “"+method.Definition.Title+"”", "It will run only when started.")
	if err != nil || waiting != nil {
		return waiting, err
	}
	if err := s.useAgentActionApproval(ctx, invocation.UserID, approval); err != nil {
		return nil, err
	}
	if err := s.database.DeleteWorkflowSchedule(ctx, invocation.UserID, id); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"unscheduled": id, "title": method.Definition.Title})
}
