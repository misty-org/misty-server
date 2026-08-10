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

func explicitMessageSendIntent(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	for _, prefix := range []string{"how do ", "how can ", "how would ", "what does ", "what happens ", "why ", "whether ", "can agents ", "can an agent ", "do agents ", "are agents "} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	lower = strings.NewReplacer("don't", "do not", "dont", "do not", "can't", "cannot", "cant", "cannot").Replace(lower)
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
	for index, token := range tokens {
		if token == "tell" && index+1 < len(tokens) && (tokens[index+1] == "me" || tokens[index+1] == "us") {
			continue
		}
		action := token == "send" || token == "text" || token == "tell" || token == "post" || token == "message" || token == "notify"
		if token == "let" {
			for next := index + 1; next < len(tokens) && next <= index+4; next++ {
				if tokens[next] == "know" {
					action = true
				}
			}
		}
		if !action {
			continue
		}
		negated := false
		for previous := max(0, index-3); previous < index; previous++ {
			if tokens[previous] == "not" || tokens[previous] == "never" || tokens[previous] == "without" || tokens[previous] == "cannot" {
				negated = true
			}
		}
		if !negated {
			return true
		}
	}
	return false
}

func TestingCompileAgentIntent(prompt string) []string {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	allowed := []string{"tasks.query"}
	if explicitMessageSendIntent(prompt) {
		allowed = append(allowed, toolboxMessagesSend)
	}
	if explicitTaskWriteIntent(lower, "create", "add", "make", "open") {
		allowed = append(allowed, "tasks.create")
	}
	if explicitTaskWriteIntent(lower, "update", "change", "mark", "set", "assign", "complete") {
		allowed = append(allowed, "tasks.update")
	}
	if explicitAgentDelegationIntent(lower) {
		allowed = append(allowed, toolboxAgentsDelegate)
	}
	return allowed
}

func explicitAgentDelegationIntent(value string) bool {
	value = strings.NewReplacer("don't", "do not", "dont", "do not", "can't", "cannot", "cant", "cannot").Replace(value)
	for _, denial := range []string{"do not delegate", "do not ask", "do not send", "cannot delegate"} {
		if strings.Contains(value, denial) {
			return false
		}
	}
	if genericAgentDelegationQuestion(value) {
		return false
	}
	for _, phrase := range []string{"delegate ", "hand this to ", "route this to ", "send this to ", "ask the agent ", "ask agent ", "ask my agent ", "ask our agent ", "ask the teammate ", "ask my teammate ", "ask our teammate "} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	tokens := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for index, token := range tokens {
		if token != "ask" {
			continue
		}
		for _, later := range tokens[index+1:] {
			if later == "to" {
				return true
			}
		}
	}
	return false
}

func genericAgentDelegationQuestion(value string) bool {
	for _, phrase := range []string{"can you delegate", "are you able to delegate", "can you route work", "what agents can", "what can agents"} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func explicitTaskWriteIntent(value string, words ...string) bool {
	value = strings.NewReplacer("don't", "do not", "dont", "do not", "can't", "cannot", "cant", "cannot").Replace(value)
	if genericTaskCapabilityQuestion(value) {
		return false
	}
	tokens := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
	hasTask := false
	for _, token := range tokens {
		if token == "task" || token == "tasks" {
			hasTask = true
		}
	}
	if !hasTask {
		return false
	}
	for index, token := range tokens {
		for _, word := range words {
			if token != word {
				continue
			}
			negated := false
			for previous := max(0, index-3); previous < index; previous++ {
				if tokens[previous] == "not" || tokens[previous] == "never" || tokens[previous] == "without" || tokens[previous] == "cannot" {
					negated = true
				}
			}
			if !negated {
				return true
			}
		}
	}
	return false
}

func genericTaskCapabilityQuestion(value string) bool {
	capabilityPhrases := []string{"what can you", "what are you able", "are you able", "can you help me", "do you support", "is it possible"}
	capabilityQuestion := false
	for _, phrase := range capabilityPhrases {
		if strings.Contains(value, phrase) {
			capabilityQuestion = true
			break
		}
	}
	if !capabilityQuestion {
		return false
	}
	// A concrete object turns a polite question into an actionable request.
	// Generic questions such as "can you create tasks in a Space?" remain read-only.
	for _, marker := range []string{" task called ", " task named ", " task titled ", " task to ", " task for "} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	return true
}

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
			Title          string     `json:"title"`
			Notes          string     `json:"notes"`
			Status         string     `json:"status"`
			Priority       string     `json:"priority"`
			AssigneeUserID string     `json:"assigneeUserId"`
			DueAt          *time.Time `json:"dueAt"`
			DueTimezone    string     `json:"dueTimezone"`
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
			input.DueTimezone = "UTC"
		}
		audience := db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}
		if actor.conversationID != "" {
			audience = db.SpaceResourceAudience{Kind: db.SpaceAudienceConversation, ConversationID: actor.conversationID}
		}
		created, err := database.CreateSpaceTask(ctx, actor.userID, db.SpaceTask{SpaceID: actor.spaceID, Title: input.Title, Notes: input.Notes, Status: input.Status, Priority: input.Priority, AssigneeUserID: input.AssigneeUserID, DueAt: input.DueAt, DueTimezone: input.DueTimezone, SourceRefs: json.RawMessage(`[]`), CreatedByUserID: actor.userID, CreatedByAgentID: actor.agentID, SourceRunID: actor.runID, AudienceKind: audience.Kind, AudienceConversationID: audience.ConversationID, AudienceCreatorUserID: actor.userID})
		if err != nil {
			return nil, err
		}
		return TestingMustAPIRawJSON(created), nil
	case "tasks.update":
		var input struct {
			ID             string     `json:"id"`
			Title          *string    `json:"title"`
			Notes          *string    `json:"notes"`
			Status         string     `json:"status"`
			Priority       string     `json:"priority"`
			AssigneeUserID *string    `json:"assigneeUserId"`
			DueAt          *time.Time `json:"dueAt"`
			DueTimezone    string     `json:"dueTimezone"`
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
		if input.DueAt != nil {
			current.DueAt = input.DueAt
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

// TestingSpaceAgentSendIsGrounded keeps a private Agent from inventing or
// paraphrasing content when it publishes into the shared Space chat.
func TestingSpaceAgentSendIsGrounded(prompt, message string) bool {
	prompt, message = normalizeGroundingText(prompt), normalizeGroundingText(message)
	return prompt != "" && message != "" && len([]rune(message)) <= db.MaxMessageChars &&
		explicitMessageSendIntent(prompt) && strings.Contains(prompt, message)
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
	return s.explicitTaskFileContext(ctx, userID, membership, task)
}
