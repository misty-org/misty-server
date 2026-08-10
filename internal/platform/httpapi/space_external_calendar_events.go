package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type reviewedCalendarEventInput struct {
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Location         string    `json:"location"`
	StartsAt         time.Time `json:"starts_at"`
	EndsAt           time.Time `json:"ends_at"`
	AllDay           bool      `json:"all_day"`
	Timezone         string    `json:"timezone"`
	CalendarSourceID string    `json:"calendar_source_id"`
}

// createReviewedExternalCalendarEvent performs only the exact provider write
// approved in the suggestion review. Private conversation data never reaches
// this boundary.
func (s *SpacesService) createReviewedExternalCalendarEvent(ctx context.Context, userID, spaceID string, input reviewedCalendarEventInput) (*db.SpaceCalendarEvent, error) {
	source, err := s.calendarSource(ctx, userID, spaceID, input.CalendarSourceID)
	if err != nil {
		return nil, err
	}
	token, tokenType, err := s.providerAccessToken(ctx, source.ConnectedByUserID, spaceID, source.IntegrationID)
	if err != nil {
		return nil, err
	}
	schedule := db.TaskSchedule{
		Title: input.Title, Description: input.Description, Location: input.Location,
		StartsAt: input.StartsAt.UTC().Format(time.RFC3339), EndsAt: input.EndsAt.UTC().Format(time.RFC3339),
		AllDay: input.AllDay, Timezone: input.Timezone,
	}
	if err := TestingValidateTaskSchedule(&schedule, source.Timezone); err != nil {
		return nil, err
	}
	payload := TestingGoogleEventPayload(schedule)
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(source.ExternalCalendarID) + "/events"
	raw, err := googleCalendarRequest(ctx, token, tokenType, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}
	var providerEvent googleCalendarEvent
	if json.Unmarshal(raw, &providerEvent) != nil || providerEvent.ID == "" {
		return nil, errors.New("google calendar event response was invalid")
	}
	startsAt, endsAt, allDay, timezone, err := parseGoogleEventTimes(providerEvent.Start, providerEvent.End, source.Timezone)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(raw)
	status := strings.TrimSpace(providerEvent.Status)
	if status != "tentative" {
		status = "confirmed"
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, providerEvent.Created)
	updatedAt, _ := time.Parse(time.RFC3339Nano, providerEvent.Updated)
	item := &db.SpaceCalendarEvent{
		ID: "calendar_event_" + uuid.NewString(), SpaceID: spaceID, SourceID: source.ID, Provider: "google", ExternalEventID: providerEvent.ID,
		Fingerprint: hex.EncodeToString(fingerprint[:]), Title: providerEvent.Summary,
		Description: providerEvent.Description, Location: providerEvent.Location,
		MeetingURL: providerEvent.HangoutLink, Organizer: providerEvent.Organizer,
		StartsAt: startsAt, EndsAt: endsAt, AllDay: allDay, Timezone: timezone, Status: status,
	}
	if !createdAt.IsZero() {
		created := createdAt.UTC()
		item.ProviderCreatedAt = &created
	}
	if !updatedAt.IsZero() {
		updated := updatedAt.UTC()
		item.ProviderUpdatedAt = &updated
	}
	if err := s.database.UpsertSpaceCalendarEvent(ctx, *item); err != nil {
		return nil, err
	}
	return item, nil
}
