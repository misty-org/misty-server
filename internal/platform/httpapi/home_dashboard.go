package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var homeAppIDs = map[string]bool{
	"home": true, "journal": true, "planner": true, "social": true, "library": true,
	"browser": true, "code": true, "files": true,
	"terminal": true, "agents": true, "marketplace": true,
}

func HomeDashboard(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		snapshot, err := database.HomeDashboard(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func RecordHomeAppActivity(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			AppID string `json:"app_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if !homeAppIDs[body.AppID] {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		if err := database.RecordAppActivity(r.Context(), userID, body.AppID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
