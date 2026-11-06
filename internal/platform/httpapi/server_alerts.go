package api

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// ServerAlerts answers GET /server/alerts: the open problems the alerts
// service recorded, for the server's owner only (403 not_server_owner for
// anyone else). The server sends no email, so the owner's app shows these.
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
