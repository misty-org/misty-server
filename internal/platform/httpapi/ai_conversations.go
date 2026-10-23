package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

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
		summaries, err := s.database.ListAgentSessions(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		items := []mistyConversation{}
		for _, summary := range summaries {
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
			WriteAIError(w, err)
			return
		}
		for _, summary := range summaries {
			if summary.ID != conversationID || summary.ConversationKind != "companion_task" {
				continue
			}
			conversation, conversationErr := s.mistyConversationFromSummary(r, userID, summary)
			if conversationErr != nil {
				WriteAIError(w, conversationErr)
				return
			}
			writeJSON(w, http.StatusOK, conversation)
			return
		}
		http.Error(w, "conversation not found", http.StatusNotFound)
	}
}
