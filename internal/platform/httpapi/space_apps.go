package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// maxAppInstallBodyBytes is an app's pinned source at its longest (64 KiB) plus the rest of the body.
const maxAppInstallBodyBytes = 72 << 10

// SpaceApps lists the apps a Space has installed.
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

// SpaceApp installs an app (PUT, which also moves an installed one to a new version) or removes it.
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
				Version     string   `json:"version"`
				Permissions []string `json:"permissions"`
				// Source is the pinned build of an app outside the reviewed catalog; the server keeps
				// it as given, for members to download the same build.
				Source json.RawMessage `json:"source"`
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

// SpaceAppRoomTicket mints a short-lived credential for one of an installed app's live documents, after
// rechecking that the person is a member and a mod approved the app's `rooms` permission.
func (s *SpacesService) SpaceAppRoomTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, appID, roomID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "appID"), chi.URLParam(r, "roomID")
		if err := s.database.SpaceAppRoomAccess(r.Context(), userID, spaceID, appID, roomID); err != nil {
			writeSpaceError(w, err)
			return
		}
		ticket, err := s.JournalCollab.MintAppRoomTicket(userID, spaceID, appID, roomID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
