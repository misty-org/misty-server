package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpaceLibraryService) PinnedCollections() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			pins, err := s.database.LibraryPinnedCollections(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"pins": pins})
		case http.MethodPut:
			var body struct {
				Targets []db.LibraryPinTarget `json:"targets"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			pins, err := s.database.SetLibraryPinnedCollections(r.Context(), userID, spaceID, body.Targets)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"pins": pins})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
