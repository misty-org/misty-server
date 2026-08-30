package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) SpaceCalendarSources() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.SpaceCalendarSources(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"sources": items})
		case http.MethodPost:
			var body struct {
				IntegrationID      string `json:"integration_id"`
				ExternalCalendarID string `json:"external_calendar_id"`
				DisplayName        string `json:"display_name"`
				Timezone           string `json:"timezone"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			calendar, err := s.googleCalendarMetadata(r.Context(), userID, spaceID, body.IntegrationID, body.ExternalCalendarID)
			if err != nil {
				writeProviderFailure(w, err)
				return
			}
			if strings.TrimSpace(body.DisplayName) == "" {
				body.DisplayName = calendar.Summary
			}
			if strings.TrimSpace(body.Timezone) == "" {
				body.Timezone = calendar.Timezone
			}
			item, err := s.database.CreateSpaceCalendarSource(r.Context(), userID, db.SpaceCalendarSource{
				SpaceID: spaceID, IntegrationID: body.IntegrationID,
				ExternalCalendarID: calendar.ID, DisplayName: body.DisplayName, Timezone: body.Timezone,
			})
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if err := s.syncGoogleCalendarSource(r.Context(), item, true); err != nil {
				_ = s.database.UpdateCalendarSourceSync(r.Context(), item.ID, "", "", "", "", "needs_attention", providerErrorCode(err), nil)
			}
			_ = s.database.SetSpaceSetupProviderStatus(r.Context(), userID, spaceID, "google", "configured")
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceCalendarSource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := s.database.DisableSpaceCalendarSource(
			r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "sourceID"),
		); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// SyncCalendarTasks keeps the existing client contract while refreshing the
// Google event mirror used by Agenda. Calendar-backed task publishing remains
// separate from this source synchronization path.
func (s *SpacesService) SyncCalendarTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		var body struct {
			SourceID string `json:"source_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		sources, err := s.database.SpaceCalendarSources(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		for index := range sources {
			source := &sources[index]
			if source.Status == "disabled" || (body.SourceID != "" && source.ID != body.SourceID) {
				continue
			}
			if syncErr := s.syncGoogleCalendarSource(r.Context(), source, true); syncErr != nil {
				_ = s.database.UpdateCalendarSourceSync(r.Context(), source.ID, source.SyncToken, "", "", "", "needs_attention", providerErrorCode(syncErr), nil)
			}
		}
		refreshed, err := s.database.SpaceCalendarSources(r.Context(), userID, spaceID)
		if err != nil {
			refreshed = sources
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": []db.SpaceTask{}, "sources": refreshed,
			"synced_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
}
