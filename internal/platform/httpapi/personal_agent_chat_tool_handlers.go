package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

// executeAgentContextGet returns the space identity and the timezone-resolved current time.
func executeAgentContextGet(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string) (json.RawMessage, error) {
	space, err := database.SpaceByID(ctx, actor.userID, actor.spaceID)
	if err != nil {
		return nil, err
	}
	timezone := agentToolTimezone(originalPrompt)
	location, _ := time.LoadLocation(timezone)
	now := time.Now().In(location)
	return TestingMustAPIRawJSON(map[string]any{"space_id": space.ID, "space_name": space.Name, "is_default": space.IsDefault, "timezone": timezone, "current_time": now.Format(time.RFC3339), "current_date": now.Format("2006-01-02")}), nil
}

// executeAgentMembersTool lists space members or resolves a member query to matches.
func executeAgentMembersTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, error) {
	members, err := database.SpaceMembers(ctx, actor.userID, actor.spaceID)
	if err != nil {
		return nil, err
	}
	if tool.Name == toolboxMembersList {
		return TestingMustAPIRawJSON(map[string]any{"members": sanitizedAgentMembers(members), "count": len(members)}), nil
	}
	var input struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.Query) == "" {
		return nil, serveragent.ErrInvalidRequest("member query is required")
	}
	matches := resolveAgentMembers(members, input.Query)
	return TestingMustAPIRawJSON(map[string]any{"matches": sanitizedAgentMembers(matches), "count": len(matches), "resolved": len(matches) == 1}), nil
}

// executeAgentMessageSend resolves the recipient and audience, then posts the message.
func executeAgentMessageSend(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string, tool serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Message         string `json:"message"`
		Audience        string `json:"audience"`
		RecipientUserID string `json:"recipientUserId"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	input.Message, input.Audience, input.RecipientUserID = strings.TrimSpace(input.Message), strings.TrimSpace(input.Audience), strings.TrimSpace(input.RecipientUserID)
	if input.Message == "" {
		return nil, serveragent.ErrInvalidRequest("message is required")
	}
	members, err := database.SpaceMembers(ctx, actor.userID, actor.spaceID)
	if err != nil {
		return nil, err
	}
	recipient, err := resolveAgentMessageRecipient(members, actor.userID, input.RecipientUserID, originalPrompt, input.Message)
	if err != nil {
		return nil, err
	}
	audience, err := resolveAgentMessageAudience(input.Audience, recipient != nil)
	if err != nil {
		return nil, err
	}
	conversationID := ""
	if audience == "private" {
		if recipient == nil {
			return nil, serveragent.ErrInvalidRequest("a private message requires one resolved recipient")
		}
		conversation, err := database.DirectMemberConversation(ctx, actor.userID, actor.spaceID, recipient.UserID)
		if err != nil {
			return nil, err
		}
		conversationID = conversation.ID
	}
	content := agentMessageContent(input.Message, recipient)
	message, err := createAgentToolMessage(ctx, database, actor, conversationID, content)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"message_id": message.ID, "space_id": actor.spaceID, "conversation_id": conversationID, "audience": audience}), nil
}

// createAgentToolMessage persists a message as Misty or as the personal agent, in a conversation or the space.
func createAgentToolMessage(ctx context.Context, database *db.Database, actor spaceConversationToolActor, conversationID string, content []db.MessageSpan) (*db.SpaceMessage, error) {
	if actor.agentID == "" && conversationID != "" {
		return database.CreateMistySpaceConversationMessageWithContent(ctx, actor.userID, actor.spaceID, conversationID, content)
	}
	if actor.agentID == "" {
		return database.CreateMistySpaceMessageWithContent(ctx, actor.userID, actor.spaceID, content)
	}
	if conversationID != "" {
		return database.CreatePersonalAgentConversationMessageWithContent(ctx, actor.userID, actor.spaceID, conversationID, actor.agentID, content)
	}
	return database.CreatePersonalAgentSpaceMessageWithContent(ctx, actor.userID, actor.spaceID, actor.agentID, content)
}

// executeSpaceSearchTool checks read permission, parses the query and dispatches to message or library search.
func executeSpaceSearchTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, error) {
	permission := db.PermissionMessagesRead
	if tool.Name == "library.search" {
		permission = db.PermissionLibraryView
	}
	allowed, err := permittedSpaceConversationRead(ctx, database, actor.userID, actor.spaceID, actor.agentID, permission)
	if err != nil || !allowed {
		if err != nil {
			return nil, err
		}
		return nil, workflowv2.ErrCapabilityDenied
	}
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Limit < 1 || input.Limit > 50 {
		input.Limit = 20
	}
	if tool.Name == "library.search" {
		return executeLibrarySearchTool(ctx, database, actor, input.Query, input.Limit)
	}
	return executeSpaceMessageSearchTool(ctx, database, actor, input.Query, input.Limit)
}

// executeLibrarySearchTool searches visible library items in the space.
func executeLibrarySearchTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, query string, limit int) (json.RawMessage, error) {
	items, err := database.LibraryItems(ctx, actor.userID, actor.spaceID, db.LibraryItemQuery{Search: query, Limit: limit, Visibility: "visible"})
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"items": items, "count": len(items)}), nil
}

// executeSpaceMessageSearchTool filters recent conversation or space messages by a case-insensitive substring.
func executeSpaceMessageSearchTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, rawQuery string, limit int) (json.RawMessage, error) {
	var messages []db.SpaceMessage
	var err error
	if actor.conversationID != "" {
		messages, err = database.SpaceConversationMessages(ctx, actor.userID, actor.spaceID, actor.conversationID, 0, 100)
	} else {
		messages, err = database.SpaceMessages(ctx, actor.userID, actor.spaceID, 0, 100)
	}
	if err != nil {
		return nil, err
	}
	matches := make([]db.SpaceMessage, 0, limit)
	query := strings.ToLower(rawQuery)
	for _, message := range messages {
		raw, _ := json.Marshal(message.Content)
		if query == "" || strings.Contains(strings.ToLower(string(raw)), query) {
			matches = append(matches, message)
			if len(matches) == limit {
				break
			}
		}
	}
	return TestingMustAPIRawJSON(map[string]any{"messages": matches, "count": len(matches)}), nil
}

// authorizeSpaceConversationTool checks the tool's task permission for the user and, for agents, the agent's grant.
func authorizeSpaceConversationTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, toolName string) error {
	permission := db.PermissionTasksView
	if toolName == "tasks.create" || toolName == "tasks.update" {
		permission = db.PermissionTasksManage
	}
	allowed, err := database.HasSpacePermission(ctx, actor.userID, actor.spaceID, permission)
	if err == nil && allowed && actor.agentID != "" {
		allowed, err = database.EffectiveAgentSpacePermission(ctx, actor.userID, actor.spaceID, actor.agentID, permission)
	}
	if err != nil {
		return err
	}
	if !allowed {
		return workflowv2.ErrCapabilityDenied
	}
	return nil
}

// executePermissionedSpaceTool authorizes and dispatches calendar and task tools; unknown names are denied.
func executePermissionedSpaceTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string, tool serveragent.ToolRequest) (json.RawMessage, error) {
	if err := authorizeSpaceConversationTool(ctx, database, actor, tool.Name); err != nil {
		return nil, err
	}
	switch tool.Name {
	case "calendar.query":
		return executeCalendarQueryTool(ctx, database, actor, tool)
	case "tasks.query":
		return executeTasksQueryTool(ctx, database, actor, tool)
	case "tasks.create":
		return executeTasksCreateTool(ctx, database, actor, originalPrompt, tool)
	case "tasks.update":
		return executeTasksUpdateTool(ctx, database, actor, tool)
	default:
		return nil, workflowv2.ErrCapabilityDenied
	}
}

// executeCalendarQueryTool lists calendar events in a window, defaulting to one month back through three months ahead.
func executeCalendarQueryTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		From *time.Time `json:"from"`
		To   *time.Time `json:"to"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	from, to := time.Now().UTC().AddDate(0, -1, 0), time.Now().UTC().AddDate(0, 3, 0)
	if input.From != nil {
		from = input.From.UTC()
	}
	if input.To != nil {
		to = input.To.UTC()
	}
	events, err := database.SpaceCalendarEvents(ctx, actor.userID, actor.spaceID, from, to)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"events": events}), nil
}

// executeTasksQueryTool searches tasks with optional status, priority, assignee and due-date filters.
func executeTasksQueryTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Query          string `json:"query"`
		Status         string `json:"status"`
		Priority       string `json:"priority"`
		AssigneeUserID string `json:"assigneeUserId"`
		From           string `json:"from"`
		To             string `json:"to"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	query := db.SpaceTaskQuery{Search: input.Query, Status: input.Status, Priority: input.Priority, AssigneeUserID: input.AssigneeUserID, Limit: 50}
	if input.From != "" {
		parsed, parseErr := time.Parse(time.RFC3339, input.From)
		if parseErr != nil {
			return nil, serveragent.ErrInvalidRequest("from must be an RFC 3339 timestamp with timezone offset")
		}
		query.DueFrom = &parsed
	}
	if input.To != "" {
		parsed, parseErr := time.Parse(time.RFC3339, input.To)
		if parseErr != nil {
			return nil, serveragent.ErrInvalidRequest("to must be an RFC 3339 timestamp with timezone offset")
		}
		query.DueTo = &parsed
	}
	if query.DueFrom != nil && query.DueTo != nil && !query.DueTo.After(*query.DueFrom) {
		return nil, serveragent.ErrInvalidRequest("to must be after from")
	}
	page, err := database.SpaceTaskPage(ctx, actor.userID, actor.spaceID, query)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(page), nil
}

// executeTasksCreateTool creates a task, scoped to the conversation when one is active.
func executeTasksCreateTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string, tool serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Title          string `json:"title"`
		Notes          string `json:"notes"`
		Status         string `json:"status"`
		Priority       string `json:"priority"`
		AssigneeUserID string `json:"assigneeUserId"`
		DueAt          string `json:"dueAt"`
		DueTimezone    string `json:"dueTimezone"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	if input.Status == "" {
		input.Status = "todo"
	}
	if input.Priority == "" {
		input.Priority = "medium"
	}
	if input.DueTimezone == "" {
		input.DueTimezone = agentToolTimezone(originalPrompt)
	}
	dueAt, dueErr := parseAgentToolTime(input.DueAt, "dueAt", input.DueTimezone)
	if dueErr != nil {
		return nil, dueErr
	}
	audience := db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}
	if actor.conversationID != "" {
		audience = db.SpaceResourceAudience{Kind: db.SpaceAudienceConversation, ConversationID: actor.conversationID}
	}
	created, err := database.CreateSpaceTask(ctx, actor.userID, db.SpaceTask{SpaceID: actor.spaceID, Title: input.Title, Notes: input.Notes, Status: input.Status, Priority: input.Priority, AssigneeUserID: input.AssigneeUserID, DueAt: dueAt, DueTimezone: input.DueTimezone, SourceRefs: json.RawMessage(`[]`), CreatedByUserID: actor.userID, CreatedByAgentID: actor.agentID, SourceRunID: agentSpaceRunSourceID(actor.runID), AudienceKind: audience.Kind, AudienceConversationID: audience.ConversationID, AudienceCreatorUserID: actor.userID})
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(created), nil
}

// executeTasksUpdateTool applies a partial update to an existing task the member can see.
func executeTasksUpdateTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		ID             string  `json:"id"`
		Title          *string `json:"title"`
		Notes          *string `json:"notes"`
		Status         string  `json:"status"`
		Priority       string  `json:"priority"`
		AssigneeUserID *string `json:"assigneeUserId"`
		DueAt          string  `json:"dueAt"`
		DueTimezone    string  `json:"dueTimezone"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
		return nil, db.ErrSpaceInvalid
	}
	current, err := database.SpaceTaskForMember(ctx, actor.userID, actor.spaceID, input.ID)
	if err != nil {
		return nil, err
	}
	if input.Title != nil {
		current.Title = *input.Title
	}
	if input.Notes != nil {
		current.Notes = *input.Notes
	}
	if input.Status != "" {
		current.Status = input.Status
	}
	if input.Priority != "" {
		current.Priority = input.Priority
	}
	if input.AssigneeUserID != nil {
		current.AssigneeUserID, current.AssigneeAgentID = *input.AssigneeUserID, ""
	}
	if input.DueAt != "" {
		dueAt, dueErr := parseAgentToolTime(input.DueAt, "dueAt", firstNonEmpty(input.DueTimezone, current.DueTimezone))
		if dueErr != nil {
			return nil, dueErr
		}
		current.DueAt = dueAt
	}
	if input.DueTimezone != "" {
		current.DueTimezone = input.DueTimezone
	}
	updated, err := database.UpdateSpaceTask(ctx, actor.userID, *current)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(updated), nil
}
