package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *SpacesService) SpaceInvitationToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(chi.URLParam(r, "token"))
		if token == "" {
			writeSpaceError(w, db.ErrSpaceInviteNotFound)
			return
		}
		tokenHash := security.HashToken(token)
		switch r.Method {
		case http.MethodGet:
			preview, err := s.database.SpaceInvitationPreview(r.Context(), tokenHash)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, preview)
		case http.MethodPost:
			userID, ok := authenticatedUser(w, r, s.database)
			if !ok {
				return
			}
			var body struct {
				Accept bool `json:"accept"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			space, err := s.database.RespondToSpaceInviteToken(
				r.Context(), userID, tokenHash, body.Accept,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if !body.Accept {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, http.StatusOK, space)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) RespondInvite(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		space, err := s.database.RespondToSpaceInvite(r.Context(), userID, chi.URLParam(r, "inviteID"), accept)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !accept {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, space)
	}
}

func (s *SpacesService) RemoveMember() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.RemoveSpaceMember(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "userID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) LeaveSpace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.LeaveSpace(r.Context(), userID, chi.URLParam(r, "spaceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) TransferOwner() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			UserID string `json:"user_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.TransferSpaceOwnership(r.Context(), userID, chi.URLParam(r, "spaceID"), body.UserID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) Messages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodDelete {
			if err := s.database.ClearEveryoneConversation(r.Context(), userID, spaceID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet {
			before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			messages, err := s.database.SpaceMessages(r.Context(), userID, spaceID, before, limit)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
			return
		}
		var body struct {
			Content          []db.MessageSpan `json:"content"`
			FileNodeIDs      []string         `json:"file_node_ids"`
			AttachmentIDs    []string         `json:"attachment_ids"`
			LibraryItemIDs   []string         `json:"library_item_ids"`
			ReplyToMessageID string           `json:"reply_to_message_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		message, agentIDs, err := s.database.CreateSpaceMessageWithReferences(r.Context(), userID, spaceID, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.LibraryItemIDs, body.ReplyToMessageID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		triggers := s.enqueueSpaceAgentMessageTriggers(r.Context(), userID, spaceID, "", message.ID, "mention", agentIDs, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.LibraryItemIDs)
		if len(agentIDs) == 0 {
			_ = s.database.QueueSpaceActionSuggestionAnalysis(r.Context(), userID, spaceID, "", message.ID)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"message": message, "triggered_runs": triggers})
	}
}

type agentMentionFailure struct {
	AgentID string `json:"agent_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func TestingAgentMentionFailureFromError(agentID string, err error) agentMentionFailure {
	code, message := spaceRunFailureFromError(err)
	return agentMentionFailure{AgentID: agentID, Code: code, Message: message}
}

func spaceRunFailureFromError(err error) (string, string) {
	var exhausted serveragent.HostedAILimitReachedError
	switch {
	case errors.Is(err, context.Canceled):
		return "request_canceled", "The run was canceled before it could start."
	case errors.As(err, &exhausted):
		return "hosted_ai_limit_reached", "This member has used all of their weekly AI agent usage."
	case errors.Is(err, db.ErrWorkflowIntegrationRequired):
		return "integration_required", "The run needs a required Space integration before it can start."
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		return "forbidden", "You no longer have permission to run this resource."
	case errors.Is(err, db.ErrAgentNotFound), errors.Is(err, db.ErrLibraryNotFound), errors.Is(err, db.ErrSpaceNotFound):
		return "resource_unavailable", "This resource is no longer available in the Space."
	case errors.Is(err, serveragent.ErrModelUnavailable):
		return "agent_model_unavailable", "This Agent's selected model is unavailable. Its owner must choose another model or Automatic."
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, db.ErrLibraryInvalid):
		return "invalid_request", "The run input or workflow definition is invalid."
	default:
		return "run_failed", "The run could not start. Try again or inspect its details in Studio."
	}
}

func (s *SpacesService) ChatAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpaceChatAgents(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": items})
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func renderMessageText(content []db.MessageSpan) string {
	var b strings.Builder
	for _, span := range content {
		if span.Type == "text" {
			b.WriteString(span.Text)
		} else if span.Label != "" {
			b.WriteString("@")
			b.WriteString(span.Label)
		}
	}
	return strings.TrimSpace(b.String())
}
