package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
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

func (s *SpacesService) SpaceAppRoomTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			GC *bool `json:"gc"`
		}
		if decodeOptionalJSON(w, r, &body, maxJSONBodyBytes) != nil {
			return
		}
		spaceID, appID, roomID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "appID"), chi.URLParam(r, "roomID")
		grant, err := s.database.SpaceAppRoomAccess(r.Context(), userID, spaceID, appID, roomID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		ticket, err := s.JournalCollab.MintAppRoomTicket(
			userID, spaceID, appID, roomID, grant.Generation, grant.Role, body.GC != nil && !*body.GC,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.database.RecordCollabRoom(r.Context(), db.CollabRoomRecord{
			Party: db.CollabPartyApp, Room: ticket.Room, SpaceID: spaceID,
			ResourceID: AppRoomResourceID(spaceID, appID, roomID), AppID: appID, UserID: userID,
			Permissions: []string{"rooms"},
		}); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
