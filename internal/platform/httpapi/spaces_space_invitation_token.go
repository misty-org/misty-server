package api

import (
	"net/http"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
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
		message, _, err := s.database.CreateSpaceMessageWithReferencesAndClientNonce(r.Context(), userID, spaceID, body.Content, body.FileNodeIDs, body.AttachmentIDs, body.LibraryItemIDs, body.ReplyToMessageID, body.ClientNonce)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
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
