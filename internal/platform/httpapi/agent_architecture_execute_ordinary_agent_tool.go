package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) executeOrdinaryAgentTool(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest) (json.RawMessage, error) {
	if tool.Name == toolboxMessagesSend {
		var input struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, db.ErrSpaceInvalid
		}
		input.Message = strings.TrimSpace(input.Message)
		var runInput struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(run.Input, &runInput) != nil ||
			!TestingSpaceAgentSendIsGrounded(runInput.Prompt, input.Message) {
			return nil, workflowv2.ErrCapabilityDenied
		}
		invocation := workflowv2.Invocation{
			RunID: run.ID, NodeID: "chat_tool_" + tool.ID, Attempt: 1,
			IdempotencyKey: "chat:" + run.ID + ":" + tool.ID,
			UserID:         run.RequestingMemberID, SpaceID: run.SpaceID, Input: tool.Arguments,
		}
		approved, err := s.database.EnsureWorkflowNodeApproval(
			ctx, run.ID, invocation.NodeID, tool.Name, tool.Arguments,
		)
		if err != nil {
			return nil, err
		}
		if !approved {
			return nil, workflowv2.ErrAwaitingApproval
		}
		return s.database.JournalWorkflowAction(
			ctx, run.ID, invocation.NodeID, invocation.IdempotencyKey,
			"space_messages", workflowv2.RiskWrite, tool.Arguments,
			func() (json.RawMessage, error) {
				var message *db.SpaceMessage
				var sendErr error
				if run.ConversationScopeKind == db.ConversationScopePrivate && run.ScopeConversationID != "" {
					message, sendErr = s.database.CreateSpaceConversationAgentMessage(ctx, run.RequestingMemberID, run.SpaceID, run.ScopeConversationID, run.AgentID, input.Message)
				} else {
					message, sendErr = s.database.CreateSpaceAgentMessage(ctx, run.RequestingMemberID, run.SpaceID, run.AgentID, input.Message)
				}
				if sendErr != nil {
					return nil, sendErr
				}
				return TestingMustAPIRawJSON(map[string]any{
					"message_id": message.ID, "space_id": run.SpaceID,
				}), nil
			},
		)
	}
	if strings.HasPrefix(tool.Name, "tasks.") || tool.Name == "calendar.query" {
		invocation := workflowv2.Invocation{RunID: run.ID, NodeID: "chat_tool_" + tool.ID, Attempt: 1, IdempotencyKey: "chat:" + run.ID + ":" + tool.ID, UserID: run.RequestingMemberID, SpaceID: run.SpaceID, Input: tool.Arguments}
		switch tool.Name {
		case "tasks.query":
			return s.taskQueryNode(ctx, run, invocation)
		case "calendar.query":
			return s.calendarQueryNode(ctx, run, invocation)
		case "tasks.create", "tasks.update":
			approved, err := s.database.EnsureWorkflowNodeApproval(ctx, run.ID, invocation.NodeID, tool.Name, tool.Arguments)
			if err != nil {
				return nil, err
			}
			if !approved {
				return nil, workflowv2.ErrAwaitingApproval
			}
			return s.database.JournalWorkflowAction(ctx, run.ID, invocation.NodeID, invocation.IdempotencyKey, "space_tasks", workflowv2.RiskWrite, tool.Arguments, func() (json.RawMessage, error) {
				if tool.Name == "tasks.create" {
					return s.createTaskNode(ctx, run, &db.SpaceStudioResource{ID: run.AgentID}, invocation)
				}
				return s.updateTaskNode(ctx, run, invocation)
			})
		}
	}
	if strings.HasPrefix(tool.Name, "provider.") {
		parts := strings.Split(tool.Name, ".")
		if len(parts) != 3 || parts[1] == "" {
			return nil, workflowv2.ErrCapabilityDenied
		}
		provider, operation := parts[1], parts[2]
		var config map[string]any
		if json.Unmarshal(tool.Arguments, &config) != nil {
			return nil, db.ErrSpaceInvalid
		}
		config["provider"], config["operation"] = provider, operation
		invocation := workflowv2.Invocation{RunID: run.ID, NodeID: "chat_tool_" + tool.ID, Attempt: 1, IdempotencyKey: "chat:" + run.ID + ":" + tool.ID, UserID: run.RequestingMemberID, SpaceID: run.SpaceID, Config: TestingMustAPIRawJSON(config), Input: tool.Arguments}
		if operation == "query" {
			return s.providerQueryNode(ctx, run, invocation)
		}
		if operation != "write" || !providerSupportsWrite(provider) {
			return nil, workflowv2.ErrCapabilityDenied
		}
		approvalInput := TestingWorkflowApprovalEnvelope(run, "provider."+provider+".write", provider, TestingFindWorkflowString(config, "connectionId", "connection_id"), TestingFindWorkflowString(config, "destination", "channel", "channelId", "channel_id"), tool.Arguments)
		approved, approvalErr := s.database.EnsureWorkflowNodeApproval(ctx, run.ID, invocation.NodeID, "provider."+provider+".write", approvalInput)
		if approvalErr != nil {
			return nil, approvalErr
		}
		if !approved {
			return nil, workflowv2.ErrAwaitingApproval
		}
		return s.database.JournalWorkflowAction(ctx, run.ID, invocation.NodeID, invocation.IdempotencyKey, provider, workflowv2.RiskWrite, tool.Arguments, func() (json.RawMessage, error) { return s.providerWriteNode(ctx, run, invocation) })
	}
	var arguments struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if json.Unmarshal(tool.Arguments, &arguments) != nil {
		return nil, db.ErrSpaceInvalid
	}
	arguments.Query = strings.TrimSpace(arguments.Query)
	if arguments.Limit < 1 || arguments.Limit > 50 {
		arguments.Limit = 20
	}
	switch tool.Name {
	case toolboxMessagesSearch, "space.search_messages":
		var messages []db.SpaceMessage
		var err error
		if run.ConversationScopeKind == db.ConversationScopePrivate && run.ScopeConversationID != "" {
			messages, err = s.database.SpaceConversationMessages(ctx, run.RequestingMemberID, run.SpaceID, run.ScopeConversationID, 0, 100)
		} else {
			messages, err = s.database.SpaceMessages(ctx, run.RequestingMemberID, run.SpaceID, 0, 100)
		}
		if err != nil {
			return nil, err
		}
		matches := make([]db.SpaceMessage, 0, arguments.Limit)
		query := strings.ToLower(arguments.Query)
		for _, message := range messages {
			raw, _ := json.Marshal(message.Content)
			if query == "" || strings.Contains(strings.ToLower(string(raw)), query) {
				matches = append(matches, message)
				if len(matches) == arguments.Limit {
					break
				}
			}
		}
		return TestingMustAPIRawJSON(map[string]any{"messages": matches, "count": len(matches)}), nil
	case "library.search":
		items, err := s.database.LibraryItems(ctx, run.RequestingMemberID, run.SpaceID, db.LibraryItemQuery{Search: arguments.Query, Limit: arguments.Limit, Visibility: "visible"})
		if err != nil {
			return nil, err
		}
		return TestingMustAPIRawJSON(map[string]any{"items": items, "count": len(items)}), nil
	default:
		return nil, workflowv2.ErrCapabilityDenied
	}
}

func providerAgentToolSchema(write bool) json.RawMessage {
	properties := map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}, "resource": map[string]any{"type": "string"}}
	if write {
		properties["destination"] = map[string]any{"type": "string"}
		properties["payload"] = map[string]any{"type": "object"}
		properties["mode"] = map[string]any{"type": "string", "enum": []string{"draft", "send"}}
	}
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties})
}

func providerSupportsWrite(provider string) bool {
	switch provider {
	case "slack", "discord":
		return true
	}
	return false
}

func spaceSearchAgentToolSchema() json.RawMessage {
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}})
}

func taskAgentToolSchema(write bool) json.RawMessage {
	properties := map[string]any{"query": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "assigneeUserId": map[string]any{"type": "string"}, "from": map[string]any{"type": "string"}, "to": map[string]any{"type": "string"}}
	if write {
		properties["id"] = map[string]any{"type": "string"}
		properties["title"] = map[string]any{"type": "string"}
		properties["notes"] = map[string]any{"type": "string"}
		properties["dueAt"] = map[string]any{"type": "string"}
		properties["dueTimezone"] = map[string]any{"type": "string"}
		properties["version"] = map[string]any{"type": "integer"}
	}
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties})
}

func (s *SpacesService) finishFailedCanonicalRun(ctx context.Context, run *db.SpaceRun, runErr error) (*db.SpaceRun, error) {
	code, message := spaceRunFailureFromError(runErr)
	failed, err := s.database.FinishSpaceRun(ctx, run.ID, "failed", TestingMustAPIRawJSON(map[string]string{"message": message}), code)
	if err != nil {
		return nil, err
	}
	return failed, nil
}

func (s *SpacesService) WorkflowVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, workflowID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "workflowID")
		if r.Method == http.MethodGet {
			items, err := s.database.WorkflowVersions(r.Context(), userID, spaceID, workflowID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"versions": items})
			return
		}
		var body struct {
			Version    string              `json:"version"`
			Metadata   db.WorkflowMetadata `json:"metadata"`
			Definition json.RawMessage     `json:"definition"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.CreateWorkflowVersion(r.Context(), userID, spaceID, workflowID, body.Version, body.Metadata, body.Definition)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) AgentVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		if r.Method == http.MethodGet {
			items, err := s.database.PublishedAgentVersions(r.Context(), userID, spaceID, agentID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"versions": items})
			return
		}
		var body struct {
			Workflows []db.AgentVersionWorkflow `json:"workflows"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.PublishAgentVersion(r.Context(), userID, spaceID, agentID, body.Workflows)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) AgentInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		instance, err := s.database.EnsureAgentInstance(r.Context(), userID, spaceID, agentID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if r.Method == http.MethodPost {
			instance, err = s.database.UpdateAgentInstance(r.Context(), userID, instance.ID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, instance)
	}
}
