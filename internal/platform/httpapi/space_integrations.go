package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/spaces"
)

type createdSpaceIntegration struct {
	db.SpaceIntegration
	Token string `json:"token"`
}

func writeSpaceIntegrationError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrSpaceIntegrationLimit) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "space_integration_limit_reached"})
		return
	}
	writeSpaceError(w, err)
}

func spaceIntegrationTarget(w http.ResponseWriter, r *http.Request) (string, bool) {
	integrationID := chi.URLParam(r, "integrationID")
	if !spaces.ValidIntegrationID(integrationID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return "", false
	}
	return integrationID, true
}

func (s *SpacesService) ListSpaceIntegrations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		integrations, err := s.database.SpaceIntegrations(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceIntegrationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"integrations": integrations})
	}
}

func (s *SpacesService) CreateSpaceIntegration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Name      string          `json:"name"`
			Avatar    json.RawMessage `json:"avatar"`
			Access    []string        `json:"access"`
			KeyPrefix *string         `json:"key_prefix"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		input := db.SpaceIntegrationInput{Name: body.Name, Avatar: body.Avatar, Access: body.Access}
		if body.KeyPrefix != nil {
			input.KeyPrefix = *body.KeyPrefix
		}
		token, hash, err := spaces.NewIntegrationToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		integration, err := s.database.CreateSpaceIntegration(r.Context(), userID, chi.URLParam(r, "spaceID"), input, hash)
		if err != nil {
			writeSpaceIntegrationError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, createdSpaceIntegration{SpaceIntegration: *integration, Token: token})
	}
}

func (s *SpacesService) UpdateSpaceIntegration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		integrationID, ok := spaceIntegrationTarget(w, r)
		if !ok {
			return
		}
		var body struct {
			Name      *string         `json:"name"`
			Avatar    json.RawMessage `json:"avatar"`
			Access    json.RawMessage `json:"access"`
			KeyPrefix json.RawMessage `json:"key_prefix"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		patch := db.SpaceIntegrationPatch{Name: body.Name, Avatar: body.Avatar}
		if body.Access != nil {
			if err := json.Unmarshal(body.Access, &patch.Access); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
				return
			}
			patch.SetAccess = true
		}
		if body.KeyPrefix != nil {
			var prefix *string
			if err := json.Unmarshal(body.KeyPrefix, &prefix); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
				return
			}
			empty := ""
			if prefix == nil {
				prefix = &empty
			}
			patch.KeyPrefix = prefix
		}
		integration, err := s.database.UpdateSpaceIntegration(r.Context(), userID, chi.URLParam(r, "spaceID"), integrationID, patch)
		if err != nil {
			writeSpaceIntegrationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, integration)
	}
}

func (s *SpacesService) ReplaceSpaceIntegrationToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		integrationID, ok := spaceIntegrationTarget(w, r)
		if !ok {
			return
		}
		token, hash, err := spaces.NewIntegrationToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		if err := s.database.ReplaceSpaceIntegrationToken(r.Context(), userID, chi.URLParam(r, "spaceID"), integrationID, hash); err != nil {
			writeSpaceIntegrationError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

func (s *SpacesService) RemoveSpaceIntegration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		integrationID, ok := spaceIntegrationTarget(w, r)
		if !ok {
			return
		}
		if err := s.database.RemoveSpaceIntegration(r.Context(), userID, chi.URLParam(r, "spaceID"), integrationID); err != nil {
			writeSpaceIntegrationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
