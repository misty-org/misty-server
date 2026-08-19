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
	toolboxLibraryRead              = "library.read"
	toolboxLibraryUpdate            = "library.update"
	toolboxLibraryPromoteAttachment = "library.promote_attachment"
)

func libraryMutationToolDescriptors() []agenttools.Descriptor {
	readSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "maxLength": 200}}, "required": []string{"id"}, "additionalProperties": false})
	updateSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string", "maxLength": 200}, "displayName": map[string]any{"type": "string", "maxLength": 255}, "caption": map[string]any{"type": "string", "maxLength": 4000}, "tags": map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string"}}, "favorite": map[string]any{"type": "boolean"}, "hidden": map[string]any{"type": "boolean"},
	}, "required": []string{"id"}, "additionalProperties": false})
	promoteSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"attachmentId": map[string]any{"type": "string", "maxLength": 200}}, "required": []string{"attachmentId"}, "additionalProperties": false})
	return []agenttools.Descriptor{
		{Name: toolboxLibraryRead, Version: 1, Description: "Read metadata, caption, tags, and file facts for one visible Library item in the current Space.", Risk: serveragent.RiskRead, InputSchema: readSchema, OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionLibraryView, AgentPermission: db.PermissionLibraryView, AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: toolboxLibraryUpdate, Version: 1, Description: "Update the name, caption, tags, favorite, or hidden state of an identified Library item.", Risk: serveragent.RiskWrite, InputSchema: updateSchema, OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionLibraryEdit, AgentPermission: db.PermissionLibraryEdit, AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "library.item.updated", Sources: agentToolboxSpaceSources},
		{Name: toolboxLibraryPromoteAttachment, Version: 1, Description: "Save an identified Space message attachment into the current Space Library.", Risk: serveragent.RiskWrite, InputSchema: promoteSchema, OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionLibraryAdd, AgentPermission: db.PermissionLibraryAdd, AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "library.item.promoted", Sources: agentToolboxSpaceSources},
	}
}

func executeAgentLibraryTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, bool, error) {
	switch tool.Name {
	case toolboxLibraryRead:
		var input struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("Library item id is required")
		}
		item, err := database.LibraryItem(ctx, actor.userID, actor.spaceID, input.ID)
		return TestingMustAPIRawJSON(item), true, err
	case toolboxLibraryUpdate:
		var input struct {
			ID          string    `json:"id"`
			DisplayName *string   `json:"displayName"`
			Caption     *string   `json:"caption"`
			Tags        *[]string `json:"tags"`
			Favorite    *bool     `json:"favorite"`
			Hidden      *bool     `json:"hidden"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("Library item id is required")
		}
		current, err := database.LibraryItem(ctx, actor.userID, actor.spaceID, input.ID)
		if err != nil {
			return nil, true, err
		}
		name, caption, tags, favorite, hidden := current.DisplayName, current.Caption, current.Tags, current.Favorite, current.Hidden
		if input.DisplayName != nil {
			name = *input.DisplayName
		}
		if input.Caption != nil {
			caption = *input.Caption
		}
		if input.Tags != nil {
			tags = *input.Tags
		}
		if input.Favorite != nil {
			favorite = *input.Favorite
		}
		if input.Hidden != nil {
			hidden = *input.Hidden
		}
		item, err := database.UpdateLibraryItem(ctx, actor.userID, actor.spaceID, current.ID, current.Version, name, caption, tags, favorite, hidden)
		return TestingMustAPIRawJSON(item), true, err
	case toolboxLibraryPromoteAttachment:
		var input struct {
			AttachmentID string `json:"attachmentId"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.AttachmentID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("attachment id is required")
		}
		item, err := database.PromoteMessageAttachment(ctx, actor.userID, actor.spaceID, input.AttachmentID)
		return TestingMustAPIRawJSON(item), true, err
	default:
		return nil, false, nil
	}
}
