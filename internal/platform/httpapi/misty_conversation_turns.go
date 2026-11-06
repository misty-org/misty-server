package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/agent"
)

// MistyConversationTurns changes which turns a conversation has: sending a prompt
// again drops it and what followed, and a Teach-mode reply can move to its lesson.
func (s *AIService) MistyConversationTurns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		var body struct {
			Action           string   `json:"action"`
			InvocationIDs    []string `json:"invocation_ids"`
			ToConversationID string   `json:"to_conversation_id"`
		}
		if decodeAIJSON(w, r, &body) != nil || len(body.InvocationIDs) > 500 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var err error
		switch body.Action {
		case "detach":
			err = s.database.DetachAIConversationTurns(r.Context(), userID, conversationID, body.InvocationIDs)
		case "move":
			if len(body.InvocationIDs) != 1 {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			err = s.database.MoveAIConversationTurn(r.Context(), userID, conversationID, body.InvocationIDs[0], strings.TrimSpace(body.ToConversationID))
		default:
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if errors.Is(err, agent.ErrPersistedSessionNotFound) {
			http.Error(w, "conversation not found", http.StatusNotFound)
			return
		}
		if err != nil {
			WriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

const ledgerMarker = "<!--misty-ledger:"

// withLedgerRows is a reply's text with the requirements ledger riding on it
// replaced; only the ledger can change, never what the reply said.
func withLedgerRows(text string, rows json.RawMessage) string {
	if start := strings.LastIndex(text, ledgerMarker); start >= 0 {
		if end := strings.Index(text[start:], "-->"); end >= 0 {
			text = strings.TrimSpace(text[:start] + text[start+end+3:])
		}
	}
	var parsed []json.RawMessage
	if json.Unmarshal(rows, &parsed) != nil || len(parsed) == 0 {
		return text
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return text
	}
	return text + "\n\n" + ledgerMarker + strings.ReplaceAll(string(encoded), "<", `<`) + "-->"
}

// MistyConversationLedger saves a reply's edited requirements ledger.
func (s *AIService) MistyConversationLedger() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			InvocationID string          `json:"invocation_id"`
			Rows         json.RawMessage `json:"rows"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.InvocationID == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err := s.database.ReplaceAIConversationReply(r.Context(), userID, strings.TrimSpace(chi.URLParam(r, "conversationID")), body.InvocationID, func(current string) string {
			return withLedgerRows(current, body.Rows)
		})
		if errors.Is(err, agent.ErrPersistedSessionNotFound) {
			http.Error(w, "conversation not found", http.StatusNotFound)
			return
		}
		if err != nil {
			WriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// MistyLessons lists a person's Teach-mode lessons and courses.
func (s *AIService) MistyLessons() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		documents, err := s.database.ListAgentLessonDocuments(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, documents)
	}
}

// MistyLesson saves or removes one lesson or course.
func (s *AIService) MistyLesson() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteAgentLessonDocument(r.Context(), userID, kind, id); err != nil {
				WriteAIError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var document json.RawMessage
		if err := DecodeAIJSONWithLimit(w, r, &document, 1<<20); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := s.database.PutAgentLessonDocument(r.Context(), userID, kind, id, document); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// InvocationToolOutput returns what a run's tools returned, for its owner. Sealed
// results come back without their output.
func (s *AIService) InvocationToolOutput() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		items, err := s.database.AIInvocationToolOutputs(r.Context(), userID, chi.URLParam(r, "invocationID"))
		if err != nil {
			WriteAIError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
