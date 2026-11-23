package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpacesService) Conversations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, everyoneUnread, err := s.database.SpaceConversationsWithUnread(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"conversations": items, "everyone_unread_count": everyoneUnread})
			return
		}
		var body struct {
			Title        string                          `json:"title"`
			Participants []db.SpaceActorRef              `json:"participants"`
			Overwrites   []db.SpaceConversationOverwrite `json:"overwrites"`
			Voice        bool                            `json:"voice"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		var item *db.SpaceConversation
		var err error
		if body.Voice {
			item, err = s.database.CreateSpaceVoiceChannel(r.Context(), userID, spaceID, body.Title, body.Overwrites)
		} else {
			item, err = s.database.CreateSpaceConversationWithOverwrites(r.Context(), userID, spaceID, body.Title, body.Participants, body.Overwrites)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) DirectConversation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			UserID  string   `json:"user_id"`
			UserIDs []string `json:"user_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		var item *db.SpaceConversation
		var err error
		if len(body.UserIDs) > 1 {
			item, err = s.database.CreateGroupDirectConversation(r.Context(), userID, chi.URLParam(r, "spaceID"), body.UserIDs)
		} else {
			target := body.UserID
			if len(body.UserIDs) == 1 {
				target = body.UserIDs[0]
			}
			item, err = s.database.DirectMemberConversation(r.Context(), userID, chi.URLParam(r, "spaceID"), target)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
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
			endVoiceChannelCall(r.Context(), spaceID, conversationID)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Title string `json:"title"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpdateSpaceConversation(r.Context(), userID, spaceID, conversationID, body.Title)
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
			ThreadRootID     string           `json:"thread_root_id"`
			ClientNonce      string           `json:"client_nonce"`
			InputModality    string           `json:"input_modality"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		var message *db.SpaceMessage
		var err error
		if body.ThreadRootID != "" {
			message, _, err = s.database.CreateSpaceThreadReplyWithClientNonce(r.Context(), userID, spaceID, conversationID, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.ThreadRootID, body.ClientNonce)
		} else {
			message, _, err = s.database.CreateSpaceConversationMessageWithReferencesAndClientNonce(r.Context(), userID, spaceID, conversationID, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.ReplyToMessageID, body.ClientNonce)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
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
