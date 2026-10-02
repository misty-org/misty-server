package api

import (
	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

func SpacePersonalItems(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := database.SpacePersonalItems(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		var body struct {
			ItemKey  string `json:"item_key"`
			Favorite *bool  `json:"favorite"`
			Opened   bool   `json:"opened"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := database.UpdateSpacePersonalItem(r.Context(), userID, spaceID, body.ItemKey, body.Favorite, body.Opened)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}
