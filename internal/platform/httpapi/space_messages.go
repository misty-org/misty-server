package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/db"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) Messages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
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
			ClientNonce      string           `json:"client_nonce"`
			InputModality    string           `json:"input_modality"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		message, agentMentions, err := s.database.CreateSpaceMessageWithReferencesAndClientNonce(r.Context(), userID, spaceID, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.LibraryItemIDs, body.ReplyToMessageID, body.ClientNonce)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		s.startAgentMentionRequests(r.Context(), userID, message, agentMentions)
		writeJSON(w, http.StatusCreated, map[string]any{"message": message})
	}
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

func (s *SpacesService) Message() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, messageID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "messageID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceMessage(r.Context(), userID, spaceID, messageID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Content     []db.MessageSpan `json:"content"`
			FileNodeIDs []string         `json:"file_node_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		message, err := s.database.UpdateSpaceMessage(r.Context(), userID, spaceID, messageID, body.Content, body.FileNodeIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, message)
	}
}

func (s *SpacesService) MessageReaction() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		messageID := chi.URLParam(r, "messageID")
		emoji := chi.URLParam(r, "emoji")
		var (
			message *db.SpaceMessage
			err     error
		)
		if r.Method == http.MethodDelete {
			message, err = s.database.RemoveSpaceMessageReaction(r.Context(), userID, spaceID, messageID, emoji)
		} else {
			message, err = s.database.AddSpaceMessageReaction(r.Context(), userID, spaceID, messageID, emoji)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, message)
	}
}

func (s *SpacesService) MarkRead() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Seq int64 `json:"seq"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.MarkSpaceRead(r.Context(), userID, chi.URLParam(r, "spaceID"), body.Seq); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
