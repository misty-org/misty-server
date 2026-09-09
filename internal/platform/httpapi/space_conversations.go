package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) Conversations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.SpaceConversations(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
			return
		}
		var body struct {
			Title        string             `json:"title"`
			Participants []db.SpaceActorRef `json:"participants"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.CreateSpaceConversation(r.Context(), userID, spaceID, body.Title, body.Participants)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) Conversation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		conversationID := chi.URLParam(r, "conversationID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteOrClearSpaceConversation(
				r.Context(), userID, spaceID, conversationID,
			); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Title        string             `json:"title"`
			Participants []db.SpaceActorRef `json:"participants"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpdateSpaceConversation(r.Context(), userID, spaceID, conversationID, body.Title, body.Participants)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) ConversationMessages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		conversationID := chi.URLParam(r, "conversationID")
		if r.Method == http.MethodGet {
			before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			messages, err := s.database.SpaceConversationMessages(r.Context(), userID, spaceID, conversationID, before, limit)
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
		var socialBinding *db.SocialBinding
		if candidate, _, socialErr := s.database.SocialManualSendContext(r.Context(), userID, spaceID, conversationID); socialErr == nil {
			socialBinding = candidate
		} else if socialErr == db.ErrSpaceForbidden || (socialErr != nil && socialErr != db.ErrSpaceNotFound) {
			writeSpaceError(w, socialErr)
			return
		}
		message, _, err := s.database.CreateSpaceConversationMessageWithReferencesAndClientNonce(r.Context(), userID, spaceID, conversationID, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.LibraryItemIDs, body.ReplyToMessageID, body.ClientNonce)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if socialBinding != nil {
			if _, queueErr := s.database.QueueSocialManualCommand(r.Context(), userID, spaceID, conversationID, body.Content, body.ClientNonce); queueErr != nil {
				writeSpaceError(w, queueErr)
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"message": message})
	}
}

func (s *SpacesService) ConversationMessage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		conversationID := chi.URLParam(r, "conversationID")
		messageID := chi.URLParam(r, "messageID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceConversationMessage(r.Context(), userID, spaceID, conversationID, messageID); err != nil {
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
		message, err := s.database.UpdateSpaceConversationMessage(r.Context(), userID, spaceID, conversationID, messageID, body.Content, body.FileNodeIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, message)
	}
}

func (s *SpacesService) ConversationMessageReaction() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		conversationID := chi.URLParam(r, "conversationID")
		messageID := chi.URLParam(r, "messageID")
		emoji := chi.URLParam(r, "emoji")
		var (
			message *db.SpaceMessage
			err     error
		)
		if r.Method == http.MethodDelete {
			message, err = s.database.RemoveSpaceConversationMessageReaction(r.Context(), userID, spaceID, conversationID, messageID, emoji)
		} else {
			message, err = s.database.AddSpaceConversationMessageReaction(r.Context(), userID, spaceID, conversationID, messageID, emoji)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, message)
	}
}
