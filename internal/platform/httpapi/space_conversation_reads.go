package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) MarkConversationRead() http.HandlerFunc {
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
		if err := s.database.MarkSpaceConversationRead(
			r.Context(), userID, chi.URLParam(r, "spaceID"),
			chi.URLParam(r, "conversationID"), body.Seq,
		); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
