package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SpaceItemVisibility flips a Space item between public and private: notes,
// drawings, Library files, folders and links, code projects and agents. Only
// the item's owner can. Channels, threads and the Space itself have no
// toggle, and DMs are always private.
//
// PUT /spaces/{spaceID}/items/{kind}/{itemID}/visibility {"visibility":"private"}
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
