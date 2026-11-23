package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) MessagePin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		err := s.database.SetSpaceMessagePinned(r.Context(), userID, chi.URLParam(r, "spaceID"),
			chi.URLParam(r, "conversationID"), chi.URLParam(r, "messageID"), r.Method == http.MethodPut)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) Pins() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpacePinnedMessages(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": items})
	}
}
