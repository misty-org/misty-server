package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) ShareResourceWithSpace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.ShareSpaceResourceWithSpace(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "resourceKind"), chi.URLParam(r, "resourceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
