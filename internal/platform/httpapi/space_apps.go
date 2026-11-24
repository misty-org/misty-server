package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

const maxAppInstallBodyBytes = 72 << 10

func (s *SpacesService) SpaceApps() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		apps, err := s.database.SpaceApps(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
	}
}

func (s *SpacesService) SpaceApp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, appID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "appID")
		switch r.Method {
		case http.MethodPut:
			var body struct {
				Version     string          `json:"version"`
				Permissions []string        `json:"permissions"`
				Source      json.RawMessage `json:"source"`
			}
			if decodeJSONUpTo(w, r, &body, maxAppInstallBodyBytes) != nil {
				return
			}
			app, err := s.database.InstallSpaceApp(r.Context(), userID, spaceID, appID, body.Version, body.Permissions, body.Source)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, app)
		case http.MethodDelete:
			if err := s.database.RemoveSpaceApp(r.Context(), userID, spaceID, appID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
