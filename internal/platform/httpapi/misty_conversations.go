package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/agent"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// MistyConversations exposes the durable Agent transcript store to its owner, in
// every Space. A conversation is always private to its owner: no one else in its
// Space, the Space's owner included, can list or read it. It never includes local
// device paths.
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
				if id := r.URL.Query().Get("agent_id"); id != "" && summary.AgentID != id {
					continue
				}
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
				AgentID         string `json:"agent_id,omitempty"`
				ModelID         string `json:"model_id,omitempty"`
				ReasoningEffort string `json:"reasoning_effort,omitempty"`
				Title           string `json:"title"`
				// SpaceID is the window's current Space; empty is the personal Space.
				SpaceID string `json:"space_id,omitempty"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			spaceID, err := s.database.MemorySpace(r.Context(), userID, body.SpaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			var identity *db.AskIdentity
			var identityErr error
			if body.AgentID == "" {
				identity, identityErr = s.database.EnsureAskIdentity(r.Context(), userID, agent.FrontierDefaultModelID())
			} else {
				identity, identityErr = s.database.AskIdentityByID(r.Context(), userID, body.AgentID)
			}
			if identityErr != nil {
				writePersonalAgentError(w, identityErr)
				return
			}
			modelID := agent.FrontierDefaultModelID()
			reasoning := agent.ManagedReasoning("", body.ReasoningEffort)
			// A conversation runs on the device's models; its runtime picks them.
			var conversationID string
			conversationID, err = s.database.CreatePersonalAgentConversation(r.Context(), userID, spaceID, identity.ID)
			if err != nil {
				WriteAIError(w, err)
				return
			}
			title := cleanMistyTitle(body.Title)
			if err := s.database.RenameAgentSession(r.Context(), userID, conversationID, title); err != nil {
				WriteAIError(w, err)
				return
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if err := s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, reasoning, agent.FrontierModelCatalogVersion); err != nil {
				WriteAIError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, mistyConversation{
				ID: conversationID, AgentID: identity.ID, Title: title, CreatedAt: now, UpdatedAt: now,
				SpaceID: spaceID, PersonalSpace: strings.TrimSpace(body.SpaceID) == "", Kind: "misty", ModelID: modelID, Reasoning: reasoning,
				Messages: []mistyConversationMessage{}, Remote: true,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
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
				WriteAIError(w, err)
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
				ThinkingMode    *string `json:"thinking_mode"`
				Title           *string `json:"title"`
				ModelID         *string `json:"model_id"`
				ReasoningEffort *string `json:"reasoning_effort"`
				// One of the conversation's own agent's folders; empty returns it to Recents.
				FolderID *string `json:"folder_id"`
				// True records that the user has now seen the conversation.
				Read *bool `json:"read"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			response := map[string]any{"id": conversationID}
			if body.FolderID != nil {
				folderID := strings.TrimSpace(*body.FolderID)
				if err := s.database.FileAgentConversation(r.Context(), userID, conversationID, folderID); err != nil {
					writeConversationFolderError(w, err)
					return
				}
				response["folderId"] = folderID
			}
			if body.Read != nil && *body.Read {
				readAt, err := s.database.MarkAgentConversationRead(r.Context(), userID, conversationID)
				if err != nil {
					WriteAIError(w, err)
					return
				}
				response["lastReadAt"] = readAt.UTC().Format(time.RFC3339Nano)
			}
			if body.Title != nil {
				title := cleanMistyTitle(*body.Title)
				if err := s.database.RenameAgentSession(r.Context(), userID, conversationID, title); err != nil {
					WriteAIError(w, err)
					return
				}
				response["title"] = title
			}
			if body.ThinkingMode != nil || body.ReasoningEffort != nil {
				bound, err := s.database.AgentConversationIdentity(r.Context(), userID, conversationID)
				if err != nil {
					WriteAIError(w, err)
					return
				}
				modelID := agent.FrontierDefaultModelID()
				mode := ""
				if body.ThinkingMode != nil {
					mode = strings.TrimSpace(*body.ThinkingMode)
					if mode != "normal" && mode != "deep" {
						writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_thinking_mode", "message": "Thinking mode must be normal or deep."})
						return
					}
				}
				legacy := bound.ReasoningEffort
				if body.ReasoningEffort != nil {
					legacy = *body.ReasoningEffort
				}
				reasoning := agent.ManagedReasoning(mode, legacy)
				if err := s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, reasoning, agent.FrontierModelCatalogVersion); err != nil {
					WriteAIError(w, err)
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
