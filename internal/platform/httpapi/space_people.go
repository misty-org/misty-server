package api

import (
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

// SpacePeople lists the people the caller already shares a Space with.
func SpacePeople(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		people, err := database.SpacePeople(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"people": people})
	}
}
