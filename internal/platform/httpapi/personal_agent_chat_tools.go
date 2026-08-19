package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

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
	planOnly       bool
}

func executeSpaceConversationTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string, tool serveragent.ToolRequest) (json.RawMessage, error) {
	if result, handled, err := executeAgentNoteTool(ctx, database, actor, tool); handled {
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
			Message string `json:"message"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, db.ErrSpaceInvalid
		}
		input.Message = strings.TrimSpace(input.Message)
		if !TestingSpaceAgentSendIsGrounded(originalPrompt, input.Message) {
			return nil, workflowv2.ErrCapabilityDenied
		}
		var message *db.SpaceMessage
		var err error
		if actor.conversationID != "" {
			message, err = database.CreateSpaceConversationAgentMessage(ctx, actor.userID, actor.spaceID, actor.conversationID, actor.agentID, input.Message)
		} else {
			message, err = database.CreatePersonalAgentSpaceMessage(ctx, actor.userID, actor.spaceID, actor.agentID, input.Message)
		}
		if err != nil {
			return nil, err
		}
		return TestingMustAPIRawJSON(map[string]any{"message_id": message.ID, "space_id": actor.spaceID}), nil
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
			Query  string `json:"query"`
			Status string `json:"status"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, db.ErrSpaceInvalid
		}
		page, err := database.SpaceTaskPage(ctx, actor.userID, actor.spaceID, db.SpaceTaskQuery{Search: input.Query, Status: input.Status, Limit: 50})
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
		created, err := database.CreateSpaceTask(ctx, actor.userID, db.SpaceTask{SpaceID: actor.spaceID, Title: input.Title, Notes: input.Notes, Status: input.Status, Priority: input.Priority, AssigneeUserID: input.AssigneeUserID, DueAt: dueAt, DueTimezone: input.DueTimezone, SourceRefs: json.RawMessage(`[]`), CreatedByUserID: actor.userID, CreatedByAgentID: actor.agentID, SourceRunID: actor.runID, AudienceKind: audience.Kind, AudienceConversationID: audience.ConversationID, AudienceCreatorUserID: actor.userID})
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
		lowerPrompt := strings.ToLower(originalPrompt)
		if !strings.Contains(lowerPrompt, strings.ToLower(current.ID)) && !strings.Contains(lowerPrompt, strings.ToLower(current.TaskKey)) {
			return nil, workflowv2.ErrCapabilityDenied
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

// TestingSpaceAgentSendIsGrounded requires an explicit member request before a
// companion may publish into shared chat. The generated message may be a
// concise paraphrase because the exact call remains approval-bound and audited.
func TestingSpaceAgentSendIsGrounded(prompt, message string) bool {
	prompt, message = normalizeGroundingText(prompt), normalizeGroundingText(message)
	if prompt == "" || message == "" || len([]rune(message)) > db.MaxMessageChars || !explicitMessageSendIntent(prompt) {
		return false
	}
	promptTokens := groundingTokenSet(prompt)
	messageTokens := groundingTokenSet(message)
	if len(messageTokens) == 0 {
		return false
	}
	overlap := 0
	for token := range messageTokens {
		if promptTokens[token] {
			overlap++
		}
	}
	return overlap*2 >= len(messageTokens)
}

func groundingTokenSet(value string) map[string]bool {
	stop := map[string]bool{"a": true, "an": true, "and": true, "are": true, "at": true, "be": true, "by": true, "can": true, "everyone": true, "for": true, "i": true, "in": true, "is": true, "it": true, "me": true, "of": true, "on": true, "please": true, "the": true, "this": true, "to": true, "will": true, "you": true}
	tokens := strings.FieldsFunc(normalizeGroundingText(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	result := map[string]bool{}
	for _, token := range tokens {
		if len([]rune(token)) > 1 && !stop[token] {
			result[token] = true
		}
	}
	return result
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
