package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	toolboxCalendarCreate = "calendar.create"
	toolboxCalendarUpdate = "calendar.update"
)

func calendarWriteToolDescriptors() []agenttools.Descriptor {
	properties := map[string]any{
		"id": map[string]any{"type": "string", "maxLength": 200}, "title": map[string]any{"type": "string", "maxLength": 240}, "description": map[string]any{"type": "string", "maxLength": 20000}, "location": map[string]any{"type": "string", "maxLength": 1000},
		"startsAt": map[string]any{"type": "string"}, "endsAt": map[string]any{"type": "string"}, "allDay": map[string]any{"type": "boolean"}, "timezone": map[string]any{"type": "string", "maxLength": 80}, "status": map[string]any{"type": "string", "enum": []string{"confirmed", "tentative", "canceled"}},
	}
	createSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": []string{"title", "startsAt", "endsAt"}, "additionalProperties": false})
	updateSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": []string{"id"}, "additionalProperties": false})
	return []agenttools.Descriptor{
		{Name: toolboxCalendarCreate, Version: 1, Description: "Create a native event in the current Space calendar.", Risk: serveragent.RiskWrite, InputSchema: createSchema, OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksManage, AgentPermission: db.PermissionTasksManage, AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, AuditEvent: "calendar.event.created", Sources: agentToolboxSpaceSources},
		{Name: toolboxCalendarUpdate, Version: 1, Description: "Update an explicitly identified native event in the current Space calendar.", Risk: serveragent.RiskWrite, InputSchema: updateSchema, OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksManage, AgentPermission: db.PermissionTasksManage, AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "calendar.event.updated", Sources: agentToolboxSpaceSources},
	}
}

type agentCalendarMutation struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartsAt    string `json:"startsAt"`
	EndsAt      string `json:"endsAt"`
	AllDay      *bool  `json:"allDay"`
	Timezone    string `json:"timezone"`
	Status      string `json:"status"`
}

func executeAgentCalendarTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, bool, error) {
	if tool.Name != toolboxCalendarCreate && tool.Name != toolboxCalendarUpdate {
		return nil, false, nil
	}
	var input agentCalendarMutation
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, true, serveragent.ErrInvalidRequest("calendar input is invalid")
	}
	var item db.SpaceCalendarEvent
	if tool.Name == toolboxCalendarUpdate {
		if strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("calendar event id is required")
		}
		current, err := database.NativeCalendarEvent(ctx, actor.userID, actor.spaceID, input.ID)
		if err != nil {
			return nil, true, err
		}
		item = *current
	}
	item.SpaceID, item.CreatedByUserID, item.CreatedByAgentID, item.SourceRunID = actor.spaceID, actor.userID, actor.agentID, actor.runID
	if strings.TrimSpace(input.Title) != "" {
		item.Title = input.Title
	}
	if input.Description != "" {
		item.Description = input.Description
	}
	if input.Location != "" {
		item.Location = input.Location
	}
	if input.Timezone != "" {
		item.Timezone = input.Timezone
	}
	if item.Timezone == "" {
		item.Timezone = "UTC"
	}
	if input.Status != "" {
		item.Status = input.Status
	}
	if input.AllDay != nil {
		item.AllDay = *input.AllDay
	}
	if input.StartsAt != "" {
		parsed, err := parseAgentToolTime(input.StartsAt, "startsAt", item.Timezone)
		if err != nil {
			return nil, true, err
		}
		item.StartsAt = *parsed
	}
	if input.EndsAt != "" {
		parsed, err := parseAgentToolTime(input.EndsAt, "endsAt", item.Timezone)
		if err != nil {
			return nil, true, err
		}
		item.EndsAt = *parsed
	}
	item.AudienceKind = db.SpaceAudienceSpace
	if tool.Name == toolboxCalendarCreate {
		created, err := database.CreateNativeCalendarEvent(ctx, actor.userID, item)
		return TestingMustAPIRawJSON(created), true, err
	}
	updated, err := database.UpdateNativeCalendarEvent(ctx, actor.userID, item)
	return TestingMustAPIRawJSON(updated), true, err
}
