package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) Facets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		facets, err := s.database.LibraryFacets(r.Context(), userID, chi.URLParam(r, "spaceID"), r.URL.Query().Get("q"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, facets)
	}
}
