package api

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"net/http"
	"strings"
)

func (s *AIService) SettingsProfiles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "profileID")
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				settingsProfileError(w, 400, "Invalid profile ID")
				return
			}
		}
		switch r.Method {
		case http.MethodGet:
			profiles, err := s.database.SettingsProfiles(r.Context(), user)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			if id != "" {
				for _, p := range profiles {
					if p.ID == id {
						writeJSON(w, 200, p)
						return
					}
				}
				settingsProfileError(w, 404, "Profile not found")
				return
			}
			writeJSON(w, 200, map[string]any{"profiles": profiles})
		case http.MethodPost:
			var body struct {
				ID     string         `json:"id"`
				Name   string         `json:"name"`
				Values map[string]any `json:"values"`
			}
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			if _, err := uuid.Parse(body.ID); err != nil {
				settingsProfileError(w, 400, "Invalid profile ID")
				return
			}
			if err := transport.ValidateProfilePatch(body.Values, nil); err != nil {
				settingsProfileError(w, 400, err.Error())
				return
			}
			body.Name = strings.TrimSpace(body.Name)
			if len([]rune(body.Name)) < 1 || len([]rune(body.Name)) > 80 {
				settingsProfileError(w, 400, "Profile name must contain 1–80 characters")
				return
			}
			p, err := s.database.CreateSettingsProfile(r.Context(), user, body.ID, body.Name, body.Values)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, 201, p)
		case http.MethodPatch:
			var patch db.SettingsProfilePatch
			if decodeAIJSON(w, r, &patch) != nil {
				return
			}
			if _, err := uuid.Parse(patch.MutationID); err != nil {
				settingsProfileError(w, 400, "Invalid mutation ID")
				return
			}
			if err := transport.ValidateProfilePatch(patch.Set, patch.Unset); err != nil {
				settingsProfileError(w, 400, err.Error())
				return
			}
			if patch.Name != nil {
				name := strings.TrimSpace(*patch.Name)
				if len([]rune(name)) < 1 || len([]rune(name)) > 80 {
					settingsProfileError(w, 400, "Profile name must contain 1–80 characters")
					return
				}
				patch.Name = &name
			}
			p, err := s.database.PatchSettingsProfile(r.Context(), user, id, patch)
			if errors.Is(err, db.ErrSettingsProfileNotFound) {
				settingsProfileError(w, 404, "Profile not found")
				return
			}
			if errors.Is(err, db.ErrSettingsProfileMutation) {
				settingsProfileError(w, 409, err.Error())
				return
			}
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, 200, p)
		case http.MethodDelete:
			if err := s.database.DeleteSettingsProfile(r.Context(), user, id); err != nil {
				TestingWriteAIError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func settingsProfileError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
