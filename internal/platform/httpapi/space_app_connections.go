package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *SpacesService) SpaceAppConnections() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		space, app := chi.URLParam(r, "spaceID"), chi.URLParam(r, "appID")
		if r.Method == http.MethodPut {
			var body struct {
				Connections []string `json:"connection_ids"`
			}
			if !decodeCapabilityRequest(w, r, &body) {
				return
			}
			if err := s.database.SetSpaceAppConnections(r.Context(), user, space, app, body.Connections); err != nil {
				writeOfficialAppError(w, err)
				return
			}
		}
		ids, err := s.database.SpaceAppConnections(r.Context(), user, space, app)
		if err != nil {
			writeOfficialAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connection_ids": ids})
	}
}
