package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) SpaceItemVisibility() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Visibility string `json:"visibility"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			return
		}
		spaceID, kind, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "kind"), chi.URLParam(r, "itemID")
		if err := s.database.SetSpaceItemVisibility(r.Context(), userID, spaceID, kind, itemID, body.Visibility); err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"kind": kind, "id": itemID, "visibility": body.Visibility})
	}
}
