package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
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
			scope := strings.TrimSpace(r.URL.Query().Get("scope"))
			deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
			items := make([]mistyConversation, 0, len(summaries))
			for _, summary := range summaries {
				if id := r.URL.Query().Get("agent_id"); id != "" && summary.AgentID != id {
					continue
				}
				if !conversationInScope(summary, scope, deviceID) {
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
				SpaceID         string `json:"space_id,omitempty"`
				// The device that starts the chat keeps it (docs/agents/chats-local.md).
				DeviceID string `json:"device_id,omitempty"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			body.SpaceID = "" // Compatibility input; personal conversations are account-owned.
			body.DeviceID = strings.TrimSpace(body.DeviceID)
			if body.DeviceID != "" && !deviceIDPattern.MatchString(body.DeviceID) {
				http.Error(w, "invalid request", http.StatusBadRequest)
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
			// Everyone runs on the house model and effort; only a development stack with
			// MISTY_ALLOW_MODEL_OVERRIDE lets a request or an agent pick (evals).
			if agent.ClientModelChoice() {
				requestedModel := strings.TrimSpace(body.ModelID)
				if identity.ModelMode == "pinned" && !identity.SystemManaged {
					requestedModel = identity.ModelID
					reasoning = agent.ManagedReasoning("", identity.ReasoningEffort)
				}
				if requestedModel != "" {
					if !agent.FrontierModelAvailable(r.Context(), requestedModel) {
						writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_model", "message": "Choose an available default model in Settings."})
						return
					}
					modelID = requestedModel
				} else if routes, err := s.database.AIModelRoutes(r.Context(), userID); err == nil {
					// No explicit pick: new chats follow the account's Thinking sense.
					for _, route := range routes {
						if route.Role == "agent" && route.Enabled && route.Model != "" && agent.FrontierModelAvailable(r.Context(), route.Model) {
							modelID = route.Model
						}
					}
				}
			}
			var conversationID string
			var err error
			conversationID, err = s.database.CreatePersonalAgentConversation(r.Context(), userID, "", identity.ID)
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
			if body.DeviceID != "" {
				if err := s.database.KeepConversationOnDevice(r.Context(), userID, conversationID, body.DeviceID); err != nil {
					WriteAIError(w, err)
					return
				}
			}
			writeJSON(w, http.StatusCreated, mistyConversation{
				ID: conversationID, AgentID: identity.ID, Title: title, CreatedAt: now, UpdatedAt: now,
				SpaceID: body.SpaceID, Kind: "misty", ModelID: modelID, Reasoning: reasoning,
				Messages: []mistyConversationMessage{}, Remote: true, KeptOnDeviceID: body.DeviceID,
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
				SpaceID         *string `json:"space_id"`
				// One of the conversation's own agent's folders; empty returns it to Recents.
				FolderID *string `json:"folder_id"`
				// True records that the user has now seen the conversation.
				Read *bool `json:"read"`
				// The device that keeps the chat from now on: one that took it from the
				// inbox, or continued a chat from before chats moved to devices.
				KeepOnDevice *string `json:"keep_on_device"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			response := map[string]any{"id": conversationID}
			if body.KeepOnDevice != nil {
				deviceID := strings.TrimSpace(*body.KeepOnDevice)
				if !deviceIDPattern.MatchString(deviceID) {
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				if err := s.database.KeepConversationOnDevice(r.Context(), userID, conversationID, deviceID); err != nil {
					if errors.Is(err, db.ErrConversationKeptElsewhere) {
						writeJSON(w, http.StatusConflict, map[string]string{"code": "kept_elsewhere", "message": "Another of your devices has this conversation."})
						return
					}
					WriteAIError(w, err)
					return
				}
				response["keptOnDeviceId"] = deviceID
			}
			if body.SpaceID != nil {
				if err := s.database.BindMistyConversationSpace(r.Context(), userID, conversationID, ""); err != nil {
					WriteAIError(w, err)
					return
				}
				response["spaceId"] = ""
			}
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
			if body.ModelID != nil && agent.ClientModelChoice() {
				// A conversation's own model; empty returns it to the account's choice.
				model := strings.TrimSpace(*body.ModelID)
				if model != "" && !agent.SenseModelAvailable(r.Context(), "thinking", model) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_model", "message": "Choose an available model."})
					return
				}
				if err := s.database.SetMistyConversationModelOverride(r.Context(), userID, conversationID, model); err != nil {
					WriteAIError(w, err)
					return
				}
				response["model_override"] = model
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

// conversationInScope filters the listing for local-first chats
// (docs/agents/chats-local.md). "legacy" is the one-time copy of chats
// from before chats moved to devices that no device has taken yet. "device" is what
// one device needs: the chats it keeps that ran in the last two days (whose runs the
// server still holds), and the inbox of chats no device keeps yet. No scope lists
// everything, for older apps.
func conversationInScope(summary db.AgentSessionSummary, scope, deviceID string) bool {
	switch scope {
	case "legacy":
		return summary.Legacy && summary.KeptOnDeviceID == ""
	case "device":
		if summary.KeptOnDeviceID == "" {
			return !summary.Legacy
		}
		return deviceID != "" && summary.KeptOnDeviceID == deviceID && summary.RecentRun
	default:
		return true
	}
}
