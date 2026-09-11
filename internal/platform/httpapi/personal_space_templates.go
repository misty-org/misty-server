package api

import (
	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

func PersonalSpaceTemplates(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		id := chi.URLParam(r, "templateID")
		if r.Method == http.MethodGet {
			items, err := database.PersonalSpaceTemplates(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"templates": items})
			return
		}
		if r.Method == http.MethodDelete {
			if err := database.DeletePersonalSpaceTemplate(r.Context(), userID, id); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			SpaceID     string `json:"space_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := database.SavePersonalSpaceTemplate(r.Context(), userID, id, body.SpaceID, body.Name, body.Description)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}
