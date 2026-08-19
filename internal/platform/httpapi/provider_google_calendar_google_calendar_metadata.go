package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type googleCalendarMetadata struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Timezone string `json:"timeZone"`
	Primary  bool   `json:"primary"`
	Access   string `json:"accessRole"`
}

type googleAPIError struct {
	Status int
	Body   string
}

func (e *googleAPIError) Error() string { return fmt.Sprintf("google calendar returned %d", e.Status) }

func (s *SpacesService) AvailableGoogleCalendars() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		connectionID := strings.TrimSpace(r.URL.Query().Get("integration_id"))
		items, err := s.listGoogleCalendars(r.Context(), userID, spaceID, connectionID)
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"calendars": items})
	}
}

func (s *SpacesService) listGoogleCalendars(ctx context.Context, userID, spaceID, integrationID string) ([]googleCalendarMetadata, error) {
	token, tokenType, err := s.providerAccessToken(ctx, userID, spaceID, integrationID)
	if err != nil {
		return nil, err
	}
	items := []googleCalendarMetadata{}
	pageToken := ""
	for pages := 0; pages < 20; pages++ {
		values := url.Values{"maxResults": {"250"}, "showDeleted": {"false"}}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		payload, err := googleCalendarRequest(ctx, token, tokenType, http.MethodGet, "https://www.googleapis.com/calendar/v3/users/me/calendarList?"+values.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items         []googleCalendarMetadata `json:"items"`
			NextPageToken string                   `json:"nextPageToken"`
		}
		if json.Unmarshal(payload, &page) != nil {
			return nil, errors.New("google calendar list response was invalid")
		}
		for _, item := range page.Items {
			if item.ID == "" || item.Access == "freeBusyReader" {
				continue
			}
			if item.Timezone == "" {
				item.Timezone = "UTC"
			}
			items = append(items, item)
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return items, nil
}

func (s *SpacesService) googleCalendarMetadata(ctx context.Context, userID, spaceID, integrationID, calendarID string) (googleCalendarMetadata, error) {
	calendarID = strings.TrimSpace(calendarID)
	if calendarID == "" {
		return googleCalendarMetadata{}, db.ErrSpaceInvalid
	}
	items, err := s.listGoogleCalendars(ctx, userID, spaceID, integrationID)
	if err != nil {
		return googleCalendarMetadata{}, err
	}
	for _, item := range items {
		if item.ID == calendarID {
			return item, nil
		}
	}
	return googleCalendarMetadata{}, db.ErrSpaceInvalid
}

type googleEventTime struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
	Timezone string `json:"timeZone"`
}

type googleCalendarEvent struct {
	ID          string          `json:"id"`
	ETag        string          `json:"etag"`
	Status      string          `json:"status"`
	Summary     string          `json:"summary"`
	Description string          `json:"description"`
	Location    string          `json:"location"`
	HTMLLink    string          `json:"htmlLink"`
	HangoutLink string          `json:"hangoutLink"`
	Organizer   json.RawMessage `json:"organizer"`
	Start       googleEventTime `json:"start"`
	End         googleEventTime `json:"end"`
	Created     string          `json:"created"`
	Updated     string          `json:"updated"`
}

func (s *SpacesService) syncGoogleCalendarSource(ctx context.Context, source *db.SpaceCalendarSource, ensureWatch bool) error {
	if source == nil || source.Status == "disabled" {
		return db.ErrSpaceInvalid
	}
	token, tokenType, err := s.providerAccessToken(ctx, source.ConnectedByUserID, source.SpaceID, source.IntegrationID)
	if err != nil {
		return err
	}
	incremental := source.SyncToken != ""
	syncToken, pageToken := source.SyncToken, ""
	finished := false
	for pages := 0; pages < 100; pages++ {
		values := url.Values{"maxResults": {"2500"}, "showDeleted": {"true"}, "singleEvents": {"true"}}
		if syncToken != "" {
			values.Set("syncToken", syncToken)
		}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		endpoint := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(source.ExternalCalendarID) + "/events?" + values.Encode()
		payload, requestErr := googleCalendarRequest(ctx, token, tokenType, http.MethodGet, endpoint, nil)
		var apiErr *googleAPIError
		if errors.As(requestErr, &apiErr) && apiErr.Status == http.StatusGone && syncToken != "" {
			if err := s.database.InvalidateSpaceCalendarEvents(ctx, source.ID, time.Now().UTC()); err != nil {
				return err
			}
			syncToken, pageToken, incremental = "", "", false
			continue
		}
		if requestErr != nil {
			return requestErr
		}
		var page struct {
			Items         []googleCalendarEvent `json:"items"`
			NextPageToken string                `json:"nextPageToken"`
			NextSyncToken string                `json:"nextSyncToken"`
		}
		if json.Unmarshal(payload, &page) != nil {
			return errors.New("google calendar events response was invalid")
		}
		for _, event := range page.Items {
			if event.ID == "" {
				continue
			}
			startsAt, endsAt, allDay, timezone, timeErr := parseGoogleEventTimes(event.Start, event.End, source.Timezone)
			if timeErr != nil {
				if event.Status == "cancelled" {
					_ = s.database.MarkSpaceCalendarEventRemoved(ctx, source.ID, event.ID, time.Now().UTC())
					if incremental {
						raw, _ := json.Marshal(event)
						fingerprint := sha256.Sum256(raw)
						_, _ = s.ProcessNormalizedProviderEvent(ctx, source.SpaceID, "google", source.ExternalCalendarID, source.DisplayName, event.ID+":"+hex.EncodeToString(fingerprint[:8]), hex.EncodeToString(fingerprint[:]), event)
					}
					continue
				}
				return timeErr
			}
			status := event.Status
			if status == "cancelled" {
				status = "canceled"
			}
			if status != "confirmed" && status != "tentative" && status != "canceled" {
				status = "confirmed"
			}
			fingerprintBytes, _ := json.Marshal(event)
			fingerprint := sha256.Sum256(fingerprintBytes)
			meetingURL := event.HangoutLink
			if meetingURL == "" && strings.HasPrefix(event.HTMLLink, "https://") {
				meetingURL = event.HTMLLink
			}
			createdAt, _ := time.Parse(time.RFC3339Nano, event.Created)
			updatedAt, _ := time.Parse(time.RFC3339Nano, event.Updated)
			var providerCreated, providerUpdated *time.Time
			if !createdAt.IsZero() {
				value := createdAt.UTC()
				providerCreated = &value
			}
			if !updatedAt.IsZero() {
				value := updatedAt.UTC()
				providerUpdated = &value
			}
			if err := s.database.UpsertSpaceCalendarEvent(ctx, db.SpaceCalendarEvent{SpaceID: source.SpaceID, SourceID: source.ID, ExternalEventID: event.ID, Fingerprint: hex.EncodeToString(fingerprint[:]), Title: event.Summary, Description: event.Description, Location: event.Location, MeetingURL: meetingURL, Organizer: event.Organizer, StartsAt: startsAt, EndsAt: endsAt, AllDay: allDay, Timezone: timezone, Status: status, ProviderCreatedAt: providerCreated, ProviderUpdatedAt: providerUpdated}); err != nil {
				return err
			}
			if incremental {
				eventID := event.ID + ":" + hex.EncodeToString(fingerprint[:8])
				_, _ = s.ProcessNormalizedProviderEvent(ctx, source.SpaceID, "google", source.ExternalCalendarID, source.DisplayName, eventID, hex.EncodeToString(fingerprint[:]), event)
			}
		}
		if page.NextPageToken != "" {
			pageToken = page.NextPageToken
			continue
		}
		if page.NextSyncToken == "" {
			return errors.New("google calendar did not return a sync token")
		}
		syncToken = page.NextSyncToken
		finished = true
		break
	}
	if !finished {
		return errors.New("google calendar synchronization exceeded the page limit")
	}
	if err := s.database.UpdateCalendarSourceSync(ctx, source.ID, syncToken, "", "", "", "active", "", nil); err != nil {
		return err
	}
	if ensureWatch && (source.WatchExpiresAt == nil || source.WatchExpiresAt.Before(time.Now().UTC().Add(24*time.Hour))) {
		return s.createGoogleCalendarWatch(ctx, source, token, tokenType, syncToken)
	}
	return nil
}
