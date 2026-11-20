package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MessagePin pins (PUT) or unpins (DELETE) a message in the Space-wide chat, a channel
// ({conversationID} in the path) or a DM (a DM's id is its Space's id).
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

// Pins lists the messages pinned in the Space-wide chat or a channel, the latest pinned first.
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
