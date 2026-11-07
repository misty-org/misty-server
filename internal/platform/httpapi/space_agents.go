package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// SpaceAgents lists the agents in a Space, each under the person who owns it.
func (s *SpacesService) SpaceAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		agents, err := s.database.SpaceAgents(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

// MySpaceAgents lists the agents in every Space the caller belongs to.
func (s *SpacesService) MySpaceAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		agents, err := s.database.SpaceAgents(r.Context(), userID, "")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

// SpaceAgent lists one of the caller's agents in a Space, or takes it out. The
// agent itself lives on the caller's computer: the Space keeps its id and name
// under its person, and nothing else about it.
func (s *SpacesService) SpaceAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		if r.Method == http.MethodDelete {
			if err := s.database.RemoveSpaceAgent(r.Context(), userID, spaceID, agentID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		agent, err := s.database.AddSpaceAgent(r.Context(), userID, spaceID, agentID, body.Name)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, agent)
	}
}
