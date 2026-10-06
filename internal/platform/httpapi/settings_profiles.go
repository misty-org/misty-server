package api

import (
	"errors"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"net/http"
)

func settingsProfileError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// AccountPreferences exposes one shared settings record for the authenticated account.
func (s *AIService) AccountPreferences() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			profile, err := s.database.AccountPreferences(r.Context(), user)
			if errors.Is(err, db.ErrSettingsProfileNotFound) {
				settingsProfileError(w, 404, "Settings not initialized")
			} else if err != nil {
				TestingWriteAIError(w, err)
			} else {
				w.Header().Set("Cache-Control", "private, no-store")
				writeJSON(w, 200, profile)
			}
			return
		}

		if r.Method == http.MethodPost {
			var body struct {
				Values map[string]any `json:"values"`
			}
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			if err := transport.ValidateProfilePatch(body.Values, nil); err != nil {
				settingsProfileError(w, 400, err.Error())
				return
			}
			p, err := s.database.EnsureAccountPreferences(r.Context(), user, body.Values)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, 200, p)
			return
		}
		var patch db.SettingsProfilePatch
		if decodeAIJSON(w, r, &patch) != nil {
			return
		}
		if _, err := uuid.Parse(patch.MutationID); err != nil {
			settingsProfileError(w, 400, "Invalid mutation ID")
			return
		}
		if patch.Name != nil {
			settingsProfileError(w, 400, "Account settings cannot be renamed")
			return
		}
		if err := transport.ValidateProfilePatch(patch.Set, patch.Unset); err != nil {
			settingsProfileError(w, 400, err.Error())
			return
		}
		p, err := s.database.PatchSettingsProfile(r.Context(), user, db.AccountPreferencesID(user), patch)
		if errors.Is(err, db.ErrSettingsProfileNotFound) {
			settingsProfileError(w, 404, "Settings not initialized")
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
	}
}
