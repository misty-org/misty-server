package api

import (
	"net/http"
	"strconv"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// HomeAgenda returns the earliest open entries across the user's Spaces in one
// request, so Home does not fetch a full agenda per Space.
func (s *SpacesService) HomeAgenda() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		from, fromErr := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
		to, toErr := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 8*24*time.Hour {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		entries, err := s.database.HomeAgenda(r.Context(), userID, from, to, limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}
