package api

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func ServerAlerts(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		owner, err := database.IsServerOwner(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !owner {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "not_server_owner"})
			return
		}
		alerts, err := database.OpenServerAlerts(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
	}
}
