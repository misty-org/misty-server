package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// SpaceCodeProjects lists (GET) the coding projects linked to a Space, or
// links (POST) one of the caller's projects to it.
func (s *SpacesService) SpaceCodeProjects() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			projects, err := s.database.SpaceCodeProjects(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
		case http.MethodPost:
			// Seeing a Space's projects is open to every member; linking one is coding.
			if !requireCoding(w, r, s.database, userID, "space.code_projects") {
				return
			}
			var input db.SpaceCodeProjectInput
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
				return
			}
			project, err := s.database.LinkSpaceCodeProject(r.Context(), userID, spaceID, input)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, project)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceCodeProject unlinks a project from a Space. Its owner or the Space's
// owner may; the files on the device are untouched.
func (s *SpacesService) SpaceCodeProject() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.UnlinkSpaceCodeProject(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "projectID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
