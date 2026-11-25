package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) SpaceAvatarDecoration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		decoration, ok := readAvatarDecoration(w, r)
		if !ok {
			return
		}
		saved, err := s.database.SetSpaceAvatarDecoration(r.Context(), userID, chi.URLParam(r, "spaceID"), decoration)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"avatar_decoration": saved})
	}
}
