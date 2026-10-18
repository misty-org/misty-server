package api

import (
	"net/http"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) DuplicateItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			ItemIDs []string `json:"item_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.ItemIDs) < 1 || len(body.ItemIDs) > 50 {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionLibraryEdit)
		if err != nil || !allowed {
			writeLibraryError(w, db.ErrLibraryForbidden)
			return
		}
		items, err := s.database.LibraryTransferItems(r.Context(), userID, spaceID, body.ItemIDs)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		// Each copy obtains account admission through the storage adapter.
		duplicated := make([]db.SpaceLibraryItem, 0, len(items))
		for _, source := range items {
			item, err := s.copyLibraryItem(r.Context(), userID, spaceID, spaceID, source)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			duplicated = append(duplicated, *item)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"items": duplicated})
	}
}
