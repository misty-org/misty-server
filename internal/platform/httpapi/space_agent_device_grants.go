package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) SpaceAgentDeviceGrants() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		if r.Method == http.MethodGet {
			items, err := s.database.AgentDeviceGrants(r.Context(), userID, spaceID, agentID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"grants": items})
			return
		}
		var body struct {
			DeviceID     string          `json:"device_id"`
			ScopeID      string          `json:"scope_id"`
			Capabilities json.RawMessage `json:"capabilities"`
			Metadata     json.RawMessage `json:"metadata"`
			ExpiresAt    time.Time       `json:"expires_at"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.GrantAgentDeviceAccess(r.Context(), userID, spaceID, agentID, body.DeviceID, body.ScopeID, body.Capabilities, body.Metadata, body.ExpiresAt)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) RevokeSpaceAgentDeviceGrant() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.RevokeAgentDeviceAccess(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID"), chi.URLParam(r, "grantID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
