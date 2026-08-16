package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func parseGoogleEventTimes(start, end googleEventTime, fallbackTimezone string) (time.Time, time.Time, bool, string, error) {
	timezone := start.Timezone
	if timezone == "" {
		timezone = fallbackTimezone
	}
	if timezone == "" {
		timezone = "UTC"
	}
	if start.DateTime != "" && end.DateTime != "" {
		startsAt, startErr := time.Parse(time.RFC3339, start.DateTime)
		endsAt, endErr := time.Parse(time.RFC3339, end.DateTime)
		if startErr != nil || endErr != nil || endsAt.Before(startsAt) {
			return time.Time{}, time.Time{}, false, timezone, errors.New("google calendar event time was invalid")
		}
		return startsAt.UTC(), endsAt.UTC(), false, timezone, nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
		timezone = "UTC"
	}
	startsAt, startErr := time.ParseInLocation("2006-01-02", start.Date, location)
	endsAt, endErr := time.ParseInLocation("2006-01-02", end.Date, location)
	if startErr != nil || endErr != nil || endsAt.Before(startsAt) {
		return time.Time{}, time.Time{}, true, timezone, errors.New("google calendar all-day event was invalid")
	}
	return startsAt.UTC(), endsAt.UTC(), true, timezone, nil
}

func (s *SpacesService) createGoogleCalendarWatch(ctx context.Context, source *db.SpaceCalendarSource, token, tokenType, syncToken string) error {
	channelID, channelToken := "gcal_"+randomProviderValue(24), randomProviderValue(32)
	expiresAt := time.Now().UTC().Add(6 * 24 * time.Hour)
	body := map[string]any{"id": channelID, "type": "web_hook", "address": TestingProviderInfrastructureURL("google", "calendar"), "token": channelToken, "expiration": expiresAt.UnixMilli()}
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(source.ExternalCalendarID) + "/events/watch"
	payload, err := googleCalendarRequest(ctx, token, tokenType, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	var response struct {
		ID         string `json:"id"`
		ResourceID string `json:"resourceId"`
		Expiration string `json:"expiration"`
	}
	if json.Unmarshal(payload, &response) != nil || response.ID == "" || response.ResourceID == "" {
		return errors.New("google calendar watch response was invalid")
	}
	if milliseconds, parseErr := strconv.ParseInt(response.Expiration, 10, 64); parseErr == nil && milliseconds > 0 {
		expiresAt = time.UnixMilli(milliseconds).UTC()
	}
	return s.database.UpdateCalendarSourceSync(ctx, source.ID, syncToken, response.ID, response.ResourceID, hashProviderValue(channelToken), "active", "", &expiresAt)
}

func (s *SpacesService) GoogleCalendarCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelID, channelToken := r.Header.Get("X-Goog-Channel-ID"), r.Header.Get("X-Goog-Channel-Token")
		resourceID := r.Header.Get("X-Goog-Resource-ID")
		if channelID == "" || channelToken == "" || resourceID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		source, err := s.database.CalendarSourceByWatchChannel(r.Context(), channelID)
		if err != nil || source.WatchTokenHash == "" || source.WatchTokenHash != hashProviderValue(channelToken) || source.WatchResourceID != resourceID {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		go func(item *db.SpaceCalendarSource) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if syncErr := s.syncGoogleCalendarSource(ctx, item, false); syncErr != nil {
				_ = s.database.UpdateCalendarSourceSync(ctx, item.ID, item.SyncToken, "", "", "", "needs_attention", providerErrorCode(syncErr), nil)
			}
		}(source)
	}
}

func (s *SpacesService) ReconcileGoogleCalendars(ctx context.Context, limit int) (int, error) {
	sources, err := s.database.CalendarSourcesNeedingReconciliation(ctx, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for index := range sources {
		if err := s.syncGoogleCalendarSource(ctx, &sources[index], true); err != nil {
			_ = s.database.UpdateCalendarSourceSync(ctx, sources[index].ID, sources[index].SyncToken, "", "", "", "needs_attention", providerErrorCode(err), nil)
			continue
		}
		completed++
	}
	return completed, nil
}

func googleCalendarRequest(ctx context.Context, token, tokenType, method, endpoint string, body any) ([]byte, error) {
	return googleCalendarRequestWithHeaders(ctx, token, tokenType, method, endpoint, body, nil)
}

func googleCalendarRequestWithHeaders(ctx context.Context, token, tokenType, method, endpoint string, body any, headers http.Header) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if tokenType == "" {
		tokenType = "Bearer"
	}
	request.Header.Set("Authorization", tokenType+" "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(payload) > 4<<20 {
		return nil, errors.New("google calendar response exceeded 4 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return payload, &googleAPIError{Status: response.StatusCode, Body: string(payload)}
	}
	return payload, nil
}

func TestingProviderInfrastructureURL(parts ...string) string {
	base := configuredPublicAPIBase()
	if base == "" {
		base = "http://127.0.0.1:8080/api"
	}
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			escaped = append(escaped, url.PathEscape(part))
		}
	}
	return base + "/provider-callbacks/" + strings.Join(escaped, "/")
}

func providerErrorCode(err error) string {
	var apiErr *googleAPIError
	status := 0
	if errors.As(err, &apiErr) {
		status = apiErr.Status
	}
	var providerErr *providerAPIError
	if errors.As(err, &providerErr) {
		status = providerErr.Status
	}
	if status != 0 {
		switch status {
		case http.StatusUnauthorized:
			return "connection_revoked"
		case http.StatusForbidden:
			return "permission_missing"
		case http.StatusTooManyRequests:
			return "rate_limited"
		case http.StatusNotFound:
			return "not_found"
		case http.StatusGone:
			return "cursor_expired"
		case http.StatusPreconditionFailed:
			return "conflict"
		}
	}
	if errors.Is(err, db.ErrSpaceConflict) {
		return "conflict"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "provider_timeout"
	}
	return "provider_unavailable"
}

func TestingProviderErrorCodeForStatus(status int) string {
	return providerErrorCode(&providerAPIError{Status: status})
}

func writeProviderFailure(w http.ResponseWriter, err error) {
	var apiErr *googleAPIError
	var providerErr *providerAPIError
	if errors.As(err, &apiErr) || errors.As(err, &providerErr) {
		status := http.StatusBadGateway
		providerStatus := 0
		if apiErr != nil {
			providerStatus = apiErr.Status
		} else if providerErr != nil {
			providerStatus = providerErr.Status
		}
		if providerStatus == http.StatusUnauthorized || providerStatus == http.StatusForbidden {
			status = http.StatusFailedDependency
		} else if providerStatus == http.StatusPreconditionFailed {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"code": providerErrorCode(err)})
		return
	}
	writeSpaceError(w, err)
}
