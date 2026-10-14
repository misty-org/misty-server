package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// AgentArtifacts lists the files agents saved to a Space, to anyone who can
// see its Library. `agent` narrows the list to one agent.
func (s *SpacesService) AgentArtifacts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := s.database.SpaceAgentArtifacts(r.Context(), userID, chi.URLParam(r, "spaceID"), r.URL.Query().Get("agent"), limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"artifacts": items})
	}
}
