package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// MistyConversations exposes the existing durable Agent transcript store as
// account-scoped Ask/Action history. It never includes local device paths.
func (s *AIService) MistyConversations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			summaries, err := s.database.ListAgentSessions(r.Context(), userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
			items := make([]mistyConversation, 0, len(summaries))
			for _, summary := range summaries {
				if query != "" && !strings.Contains(strings.ToLower(summary.Title), query) {
					continue
				}
				conversation, err := s.mistyConversationFromSummary(r, userID, summary)
				if err != nil {
					continue
				}
				items = append(items, conversation)
			}
			writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
		case http.MethodPost:
			var body struct {
				Title   string `json:"title"`
				SpaceID string `json:"space_id,omitempty"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			body.SpaceID = strings.TrimSpace(body.SpaceID)
			if body.SpaceID != "" {
				if _, err := s.database.SpaceByID(r.Context(), userID, body.SpaceID); err != nil {
					writeSpaceError(w, err)
					return
				}
			}
			conversationID, err := s.database.CreateAIConversation(r.Context(), userID, body.SpaceID)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			title := cleanMistyTitle(body.Title)
			if err := s.database.RenameAgentSession(r.Context(), userID, conversationID, title); err != nil {
				TestingWriteAIError(w, err)
				return
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			modelID := agent.FrontierDefaultModelID()
			_ = s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, "", agent.FrontierModelCatalogVersion)
			writeJSON(w, http.StatusCreated, mistyConversation{
				ID: conversationID, Title: title, CreatedAt: now, UpdatedAt: now,
				SpaceID: body.SpaceID, Kind: "misty", ModelID: modelID,
				Messages: []mistyConversationMessage{}, Remote: true,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *AIService) AIConversations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		summaries, err := s.database.ListAgentSessions(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		items := []mistyConversation{}
		for _, summary := range summaries {
			if summary.ConversationKind != "companion_task" || summary.PersonalAgentID != agentID {
				continue
			}
			conversation, conversationErr := s.mistyConversationFromSummary(r, userID, summary)
			if conversationErr == nil {
				items = append(items, conversation)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
	}
}

func (s *AIService) AIConversation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		summaries, err := s.database.ListAgentSessions(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		for _, summary := range summaries {
			if summary.ID != conversationID || summary.ConversationKind != "companion_task" {
				continue
			}
			conversation, conversationErr := s.mistyConversationFromSummary(r, userID, summary)
			if conversationErr != nil {
				TestingWriteAIError(w, conversationErr)
				return
			}
			writeJSON(w, http.StatusOK, conversation)
			return
		}
		http.Error(w, "conversation not found", http.StatusNotFound)
	}
}

func (s *AIService) MistyConversation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if conversationID == "" {
			http.Error(w, "conversation is required", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			keys, _ := s.database.AIConversationAttachmentObjectKeys(r.Context(), userID, conversationID)
			if err := s.database.DeleteAgentConversation(r.Context(), userID, conversationID); err != nil {
				TestingWriteAIError(w, err)
				return
			}
			if s.attachmentStore != nil {
				for _, key := range keys {
					_ = s.attachmentStore.Delete(r.Context(), key)
				}
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			var body struct {
				Title           *string `json:"title"`
				ModelID         *string `json:"model_id"`
				ReasoningEffort *string `json:"reasoning_effort"`
				SpaceID         *string `json:"space_id"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			response := map[string]any{"id": conversationID}
			if body.SpaceID != nil {
				spaceID := strings.TrimSpace(*body.SpaceID)
				if spaceID == "" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "space_required", "message": "Choose a Space for this conversation."})
					return
				}
				if _, err := s.database.SpaceByID(r.Context(), userID, spaceID); err != nil {
					writeSpaceError(w, err)
					return
				}
				if err := s.database.BindMistyConversationSpace(r.Context(), userID, conversationID, spaceID); err != nil {
					writeMistyConversationBindingError(w, err)
					return
				}
				response["spaceId"] = spaceID
			}
			if body.Title != nil {
				title := cleanMistyTitle(*body.Title)
				if err := s.database.RenameAgentSession(r.Context(), userID, conversationID, title); err != nil {
					TestingWriteAIError(w, err)
					return
				}
				response["title"] = title
			}
			if body.ModelID != nil || body.ReasoningEffort != nil {
				bound, err := s.database.AgentConversationIdentity(r.Context(), userID, conversationID)
				if err != nil {
					TestingWriteAIError(w, err)
					return
				}
				modelID := bound.ModelID
				if body.ModelID != nil {
					modelID = strings.TrimSpace(*body.ModelID)
				}
				if modelID == "" || !agent.FrontierModelAvailable(r.Context(), modelID) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "model_unavailable", "message": "Choose an available Misty model."})
					return
				}
				reasoning := bound.ReasoningEffort
				if body.ReasoningEffort != nil {
					reasoning = strings.TrimSpace(*body.ReasoningEffort)
				}
				if reasoning != "" && reasoning != "low" && reasoning != "medium" && reasoning != "high" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_reasoning_effort", "message": "Reasoning must be low, medium, high, or default."})
					return
				}
				if reasoning != "" && !agent.GatewayModelSupportsReasoning(r.Context(), modelID) {
					reasoning = ""
				}
				if err := s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, reasoning, agent.FrontierModelCatalogVersion); err != nil {
					TestingWriteAIError(w, err)
					return
				}
				response["model_id"] = modelID
				response["reasoning_effort"] = reasoning
			}
			writeJSON(w, http.StatusOK, response)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *AIService) MistyConversationTurn() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		var body struct {
			Mode     string                  `json:"mode"`
			Prompt   string                  `json:"prompt"`
			Context  []mistyContextReference `json:"context"`
			AgentID  string                  `json:"agent_id,omitempty"`
			Timezone string                  `json:"timezone,omitempty"`
		}
		if err := decodeAIJSON(w, r, &body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		body.Prompt = strings.TrimSpace(body.Prompt)
		if conversationID == "" || body.Prompt == "" {
			http.Error(w, "conversation and prompt are required", http.StatusBadRequest)
			return
		}
		if body.Mode == "action" {
			s.mistyActionProposal(w, r, userID, conversationID, body.Prompt, body.AgentID)
			return
		}
		if body.Mode != "ask" {
			http.Error(w, "mode must be ask or action", http.StatusBadRequest)
			return
		}
		available, err := s.database.AIActionAvailable(r.Context(), userID, "global", "ask", agent.InitialSelectedModelID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if !available {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "ai_surface_unavailable", "message": "Misty Ask is temporarily unavailable."})
			return
		}
		if !s.agentRuntime.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "agent_runtime_unavailable", "message": "Misty's agent runtime is not configured."})
			return
		}
		invocationBody := aiInvocationInput{
			Mode: "drawer", SurfaceID: "global", Trigger: "explicit", Prompt: body.Prompt,
			Context: mistyAIContextReferences(body.Context), ConversationID: conversationID,
			IdempotencyKey: "misty-turn:" + uuid.NewString(), Timezone: body.Timezone,
		}
		if err := validateAIInvocationInput(&invocationBody); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_invocation", "message": err.Error()})
			return
		}
		payload, _ := json.Marshal(invocationBody)
		now := time.Now().UTC()
		stored, _, err := s.database.CreateAIInvocationRecord(r.Context(), db.AIInvocationRecord{
			ID: "invocation_" + uuid.NewString(), UserID: userID, ConversationID: conversationID,
			SurfaceID: "global", Mode: "drawer", Trigger: "explicit", State: "queued",
			IdempotencyKey: invocationBody.IdempotencyKey, RequestPayload: payload, ExpiresAt: now.Add(aiInvocationTTL),
		})
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if _, err := s.invocations.restoreDurable(r.Context(), stored); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if _, err := s.agentRuntime.Start(r.Context(), stored.ID); err != nil {
			s.invocations.fail(stored.ID, "Misty could not start the agent runtime. Please try again.")
			TestingWriteAIError(w, err)
			return
		}
		answer, citations, err := s.awaitAIInvocationAnswer(r, userID, stored.ID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		_ = s.database.RenameAgentSession(r.Context(), userID, conversationID, cleanMistyTitle(body.Prompt))
		message := mistyConversationMessage{
			ID: "message_" + uuid.NewString(), Role: "assistant", Mode: "ask",
			Content: answer, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		writeJSON(w, http.StatusOK, map[string]any{"text": answer, "message": message, "citations": citations})
	}
}

func (s *AIService) mistyActionProposal(w http.ResponseWriter, r *http.Request, userID, conversationID, prompt, agentID string) {
	readOnly := mistyReadOnlyAction(prompt)
	title := "Review this action"
	risk := "write"
	summary := "Misty will delegate this request after you confirm."
	if readOnly {
		title = "Run with Misty"
		risk = "read"
		summary = "Misty will delegate this read-only request now."
	}
	proposalID := "proposal_" + uuid.NewString()
	_ = s.database.RenameAgentSession(r.Context(), userID, conversationID, cleanMistyTitle(prompt))
	writeJSON(w, http.StatusOK, map[string]any{
		"action": map[string]any{
			"id": proposalID, "title": title, "summary": summary, "prompt": prompt,
			"risk": risk, "state": "proposed", "requiresConfirmation": !readOnly,
			"agentId": strings.TrimSpace(agentID),
		},
	})
}

func mistyReadOnlyAction(prompt string) bool {
	first, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(prompt)), " ")
	switch strings.Trim(first, ",.:;!?") {
	case "find", "search", "show", "list", "summarize", "summarise", "explain", "review", "check":
		return true
	default:
		return false
	}
}

func (s *AIService) mistyConversationFromSummary(r *http.Request, userID string, summary db.AgentSessionSummary) (mistyConversation, error) {
	turns, err := s.database.AIConversationTurns(r.Context(), userID, summary.ID)
	if err != nil {
		return mistyConversation{}, err
	}
	if len(turns) > 0 {
		messages := make([]mistyConversationMessage, 0, len(turns)*2)
		for _, turn := range turns {
			mode := "ask"
			if turn.AgentRunID != "" {
				mode = "action"
			}
			attachments := []mistyConversationAttachment{}
			if stored, attachmentErr := s.database.AIConversationAttachmentsForInvocation(r.Context(), userID, turn.InvocationID); attachmentErr == nil {
				for _, item := range stored {
					attachments = append(attachments, mistyConversationAttachment{
						ID: item.ID, Name: item.DisplayName, MIMEType: item.MIMEType, ByteSize: item.ByteSize,
						Width: item.Width, Height: item.Height,
						PreviewURL: "/misty/attachments/" + item.ID + "/content?variant=model", State: "ready",
					})
				}
			}
			if prompt := publicMistyConversationContent(turn.Prompt); prompt != "" || len(attachments) > 0 {
				messages = append(messages, mistyConversationMessage{
					ID: turn.InvocationID + "-user", Role: "user", Mode: mode,
					Content: prompt, CreatedAt: turn.CreatedAt.UTC().Format(time.RFC3339Nano), State: "completed", Attachments: attachments,
				})
			}
			response := strings.TrimSpace(turn.Reply)
			if response == "" {
				response = strings.TrimSpace(turn.Failure)
			}
			if response == "" {
				response = strings.TrimSpace(turn.AgentError)
			}
			if response == "" && turn.AgentRunID != "" {
				response = strings.TrimSpace(turn.Status)
				if response == "" {
					response = "Misty is working on this task."
				}
			}
			if response == "" && turn.State == "canceled" {
				response = "This task was canceled."
			}
			if response != "" {
				var action *mistyConversationAction
				if turn.AgentRunID != "" {
					resultHref := "/agents?conversation=" + summary.ID
					if turn.ResultSpaceID != "" && turn.ResultDrawingID != "" {
						resultHref = "/spaces/" + turn.ResultSpaceID + "/drawings/" + turn.ResultDrawingID
					}
					action = &mistyConversationAction{
						ID: turn.InvocationID + "-action", Title: "Misty task",
						Summary: response, Prompt: turn.Prompt, Risk: "write",
						State:      mistyRunActionState(turn.AgentState),
						RunID:      turn.AgentRunID,
						ResultHref: resultHref,
						Error:      turn.AgentError,
					}
				}
				messages = append(messages, mistyConversationMessage{
					ID: turn.InvocationID + "-assistant", Role: "assistant", Mode: mode,
					Content: response, CreatedAt: turn.ReplyAt.UTC().Format(time.RFC3339Nano),
					State:     mistyTurnMessageState(turn.State, turn.AgentState),
					Retryable: turn.State == "failed" || turn.AgentState == "failed", Action: action,
				})
			}
		}
		modelID := summary.ModelID
		if modelID == "" || !agent.FrontierModelAvailable(r.Context(), modelID) {
			modelID = agent.FrontierDefaultModelID()
		}
		return mistyConversation{
			ID: summary.ID, Title: cleanMistyTitle(summary.Title), AgentID: summary.PersonalAgentID,
			SpaceID: summary.SpaceID, Kind: summary.ConversationKind, OriginSurface: summary.OriginSurface,
			OriginHref: summary.OriginHref, Privacy: summary.PrivacyBoundary, ModelID: modelID, Reasoning: summary.ReasoningEffort,
			CreatedAt: summary.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt: summary.UpdatedAt.UTC().Format(time.RFC3339Nano), Messages: messages, Remote: true,
		}, nil
	}
	transcript, err := s.runtime.Transcript(r.Context(), summary.ID, userID)
	if err != nil {
		return mistyConversation{}, err
	}
	messages := make([]mistyConversationMessage, 0, len(transcript))
	for index, item := range transcript {
		role := "assistant"
		if item.Role == agent.RoleUser {
			role = "user"
		}
		content := strings.TrimSpace(item.Content)
		if role == "user" {
			content = publicMistyConversationContent(content)
		}
		if content == "" {
			continue
		}
		messages = append(messages, mistyConversationMessage{
			ID: fmt.Sprintf("%s-%d", summary.ID, index), Role: role, Mode: "ask",
			Content: content, CreatedAt: summary.UpdatedAt.UTC().Format(time.RFC3339Nano), State: "completed",
		})
	}
	modelID := summary.ModelID
	if modelID == "" || !agent.FrontierModelAvailable(r.Context(), modelID) {
		modelID = agent.FrontierDefaultModelID()
	}
	return mistyConversation{
		ID: summary.ID, Title: cleanMistyTitle(summary.Title), AgentID: summary.PersonalAgentID,
		SpaceID: summary.SpaceID, Kind: summary.ConversationKind, OriginSurface: summary.OriginSurface,
		OriginHref: summary.OriginHref, Privacy: summary.PrivacyBoundary, ModelID: modelID, Reasoning: summary.ReasoningEffort,
		CreatedAt: summary.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: summary.UpdatedAt.UTC().Format(time.RFC3339Nano), Messages: messages, Remote: true,
	}, nil
}
