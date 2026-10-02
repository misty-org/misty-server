package api

import (
	"net/http"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

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
