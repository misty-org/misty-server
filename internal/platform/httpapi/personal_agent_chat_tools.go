package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

const toolboxMessagesSend = "messages.send"

func permittedSpaceConversationRead(ctx context.Context, database *db.Database, userID, spaceID, agentID, permission string) (bool, error) {
	allowed, err := database.HasSpacePermission(ctx, userID, spaceID, permission)
	if err != nil || !allowed || agentID == "" {
		return allowed, err
	}
	return database.EffectiveAgentSpacePermission(ctx, userID, spaceID, agentID, permission)
}

// TestingSpaceConversationToolNames exposes the server-owned manifest decision
// without exposing an executor. Contract tests use it to keep private chat and
// mentioned-Agent capability routing aligned.
func TestingSpaceConversationToolNames(prompt string) []string {
	requested := append([]string{toolboxMessagesSearch, toolboxLibrarySearch}, TestingCompileAgentIntent(prompt)...)
	explicit := map[string]bool{}
	for _, name := range requested {
		explicit[name] = true
	}
	manifest, _ := spaceAgentToolbox(nil).Resolve(context.Background(), agenttools.Invocation{Source: "space_conversation", Trigger: "message", ExplicitTools: explicit}, requested, nil)
	return manifestToolNames(manifest)
}

func TestingSpaceConversationPlanningToolNames(prompt string) []string {
	requested := append([]string{toolboxMessagesSearch, toolboxLibrarySearch}, TestingCompileAgentIntent(prompt)...)
	toolbox := spaceAgentToolbox(nil)
	requested = readOnlyToolRequests(toolbox, requested)
	explicit := map[string]bool{}
	for _, name := range requested {
		explicit[name] = true
	}
	manifest, _ := toolbox.Resolve(context.Background(), agenttools.Invocation{Source: "space_conversation", Trigger: "message", ExplicitTools: explicit}, requested, nil)
	return manifestToolNames(manifest)
}

func TestingExecuteSpaceConversationTaskTool(ctx context.Context, database *db.Database, userID, spaceID, agentID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return TestingExecuteSpaceConversationTool(ctx, database, userID, spaceID, agentID, prompt, name, arguments)
}

func TestingExecuteSpaceConversationTool(ctx context.Context, database *db.Database, userID, spaceID, agentID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	toolbox := spaceAgentToolbox(database)
	invocation := agenttools.Invocation{
		UserID: userID, SpaceID: spaceID, AgentID: agentID, Source: "space_conversation", Trigger: "message", OriginalInput: prompt,
		SessionID: "testing:" + userID + ":" + spaceID, ExplicitTools: map[string]bool{name: true},
	}
	return executeSpaceAgentToolbox(ctx, toolbox, invocation, database, serveragent.ToolRequest{Name: name, Arguments: arguments})
}

type spaceConversationToolActor struct {
	userID         string
	spaceID        string
	agentID        string
	runID          string
	sessionID      string
	conversationID string
	originalPrompt string
	planOnly       bool
}

func executeSpaceConversationTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string, tool serveragent.ToolRequest) (json.RawMessage, error) {
	actor.originalPrompt = originalPrompt
	if result, handled, err := executeAgentMemoryTool(ctx, database, actor, originalPrompt, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentNoteTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentDrawingTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentCalendarTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentRoadmapTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentLibraryTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeCompanionReadTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if tool.Name == toolboxContextGet {
		space, err := database.SpaceByID(ctx, actor.userID, actor.spaceID)
		if err != nil {
			return nil, err
		}
		timezone := agentToolTimezone(originalPrompt)
		location, _ := time.LoadLocation(timezone)
		now := time.Now().In(location)
		return TestingMustAPIRawJSON(map[string]any{"space_id": space.ID, "space_name": space.Name, "space_kind": space.Kind, "timezone": timezone, "current_time": now.Format(time.RFC3339), "current_date": now.Format("2006-01-02")}), nil
	}
	if tool.Name == toolboxMembersList || tool.Name == toolboxMembersResolve {
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
	if tool.Name == toolboxMessagesSend {
		var input struct {
			Message         string `json:"message"`
			Audience        string `json:"audience"`
			RecipientUserID string `json:"recipientUserId"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, db.ErrSpaceInvalid
		}
		input.Message, input.Audience, input.RecipientUserID = strings.TrimSpace(input.Message), strings.TrimSpace(input.Audience), strings.TrimSpace(input.RecipientUserID)
		if !TestingSpaceAgentSendIsGrounded(originalPrompt, input.Message) {
			return nil, workflowv2.ErrCapabilityDenied
		}
		members, err := database.SpaceMembers(ctx, actor.userID, actor.spaceID)
		if err != nil {
			return nil, err
		}
		recipient, err := resolveAgentMessageRecipient(members, actor.userID, input.RecipientUserID, originalPrompt, input.Message)
		if err != nil {
			return nil, err
		}
		audience, err := resolveAgentMessageAudience(originalPrompt, input.Audience, recipient != nil)
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
		var message *db.SpaceMessage
		if actor.agentID == "" && conversationID != "" {
			message, err = database.CreateMistySpaceConversationMessageWithContent(ctx, actor.userID, actor.spaceID, conversationID, content)
		} else if actor.agentID == "" {
			message, err = database.CreateMistySpaceMessageWithContent(ctx, actor.userID, actor.spaceID, content)
		} else if conversationID != "" {
			message, err = database.CreatePersonalAgentConversationMessageWithContent(ctx, actor.userID, actor.spaceID, conversationID, actor.agentID, content)
		} else {
			message, err = database.CreatePersonalAgentSpaceMessageWithContent(ctx, actor.userID, actor.spaceID, actor.agentID, content)
		}
		if err != nil {
			return nil, err
		}
		return TestingMustAPIRawJSON(map[string]any{"message_id": message.ID, "space_id": actor.spaceID, "conversation_id": conversationID, "audience": audience}), nil
	}
	if tool.Name == "space.search_messages" || tool.Name == "library.search" {
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
			items, err := database.LibraryItems(ctx, actor.userID, actor.spaceID, db.LibraryItemQuery{Search: input.Query, Limit: input.Limit, Visibility: "visible"})
			if err != nil {
				return nil, err
			}
			return TestingMustAPIRawJSON(map[string]any{"items": items, "count": len(items)}), nil
		}
		var messages []db.SpaceMessage
		if actor.conversationID != "" {
			messages, err = database.SpaceConversationMessages(ctx, actor.userID, actor.spaceID, actor.conversationID, 0, 100)
		} else {
			messages, err = database.SpaceMessages(ctx, actor.userID, actor.spaceID, 0, 100)
		}
		if err != nil {
			return nil, err
		}
		matches := make([]db.SpaceMessage, 0, input.Limit)
		query := strings.ToLower(input.Query)
		for _, message := range messages {
			raw, _ := json.Marshal(message.Content)
			if query == "" || strings.Contains(strings.ToLower(string(raw)), query) {
				matches = append(matches, message)
				if len(matches) == input.Limit {
					break
				}
			}
		}
		return TestingMustAPIRawJSON(map[string]any{"messages": matches, "count": len(matches)}), nil
	}

	permission := db.PermissionTasksView
	if tool.Name == "tasks.create" || tool.Name == "tasks.update" {
		permission = db.PermissionTasksManage
	}
	allowed, err := database.HasSpacePermission(ctx, actor.userID, actor.spaceID, permission)
	if err == nil && allowed && actor.agentID != "" {
		allowed, err = database.EffectiveAgentSpacePermission(ctx, actor.userID, actor.spaceID, actor.agentID, permission)
	}
	if err != nil || !allowed {
		if err != nil {
			return nil, err
		}
		return nil, workflowv2.ErrCapabilityDenied
	}
	switch tool.Name {
	case "calendar.query":
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
	case "tasks.query":
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
	case "tasks.create":
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
	case "tasks.update":
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
		if err := requireAgentMutationTarget(ctx, database, actor, originalPrompt, "task", current.ID, current.TaskKey); err != nil {
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
	default:
		return nil, workflowv2.ErrCapabilityDenied
	}
}

func agentSpaceRunSourceID(runID string) string {
	if isAIInvocationRuntimeID(runID) {
		return ""
	}
	return runID
}

func agentToolTimezone(originalInput string) string {
	var envelope struct {
		Timezone string `json:"timezone"`
	}
	if json.Unmarshal([]byte(originalInput), &envelope) == nil {
		timezone := strings.TrimSpace(envelope.Timezone)
		if timezone != "" {
			if _, err := time.LoadLocation(timezone); err == nil {
				return timezone
			}
		}
	}
	return "UTC"
}

func TestingAgentToolTimezone(originalInput string) string {
	return agentToolTimezone(originalInput)
}

func parseAgentToolTime(value, field, timezone string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	location, locationErr := time.LoadLocation(firstNonEmpty(strings.TrimSpace(timezone), "UTC"))
	if locationErr != nil {
		return nil, serveragent.ErrInvalidRequest("timezone must be a valid IANA timezone such as America/Los_Angeles")
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if local, localErr := time.ParseInLocation(layout, value, location); localErr == nil {
			local = local.UTC()
			return &local, nil
		}
	}
	if local, localErr := time.ParseInLocation("2006-01-02", value, location); localErr == nil {
		// A date-only due date means the end of that day in the creator's
		// timezone, rather than midnight UTC (which can display a day early).
		local = time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 0, 0, location).UTC()
		return &local, nil
	}
	return nil, serveragent.ErrInvalidRequest(field + " must be an ISO 8601 date or timestamp")
}

func TestingParseAgentToolTime(value, timezone string) (*time.Time, error) {
	return parseAgentToolTime(value, "dueAt", timezone)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizedAgentMembers(members []db.SpaceMember) []map[string]string {
	items := make([]map[string]string, 0, len(members))
	for _, member := range members {
		items = append(items, map[string]string{"user_id": member.UserID, "name": member.Name, "role": member.Role})
	}
	return items
}

func resolveAgentMembers(members []db.SpaceMember, query string) []db.SpaceMember {
	wanted := normalizeGroundingText(query)
	exact := []db.SpaceMember{}
	partial := []db.SpaceMember{}
	for _, member := range members {
		name := normalizeGroundingText(member.Name)
		email := normalizeGroundingText(member.Email)
		if wanted == name || wanted == email {
			exact = append(exact, member)
			continue
		}
		if strings.Contains(name, wanted) || strings.Contains(email, wanted) {
			partial = append(partial, member)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func (s *SpacesService) explicitMessageFileContext(ctx context.Context, userID string, membership *db.SpaceAgentMembership, spaceID string, attachmentIDs, libraryItemIDs []string) (string, string, []workflowv2.ContentRef) {
	refs := make([]explicitTaskSourceRef, 0, len(attachmentIDs)+len(libraryItemIDs))
	for _, id := range attachmentIDs {
		refs = append(refs, explicitTaskSourceRef{Kind: "chat_attachment", ResourceID: id})
	}
	for _, id := range libraryItemIDs {
		refs = append(refs, explicitTaskSourceRef{Kind: "library_item", ResourceID: id})
	}
	raw, _ := json.Marshal(refs)
	task := &db.SpaceTask{ID: "message", SpaceID: spaceID, SourceRefs: raw}
	return s.explicitTaskFileContext(ctx, userID, task)
}
