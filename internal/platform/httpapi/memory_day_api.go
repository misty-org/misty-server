package api

import (
	"net/http"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// MemoryDaySummary recaps one day of the person's Memory in one Space, from that
// Space's moments only.
// POST {space_id, date: "2026-10-14", time_zone: "Europe/Lisbon", language} gathers
// the moments for the desktop to write from; the same call with markdown keeps what
// it wrote in the tree's Summaries.
func (s *SpacesService) MemoryDaySummary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		var body struct {
			SpaceID  string          `json:"space_id"`
			Date     string          `json:"date"`
			TimeZone string          `json:"time_zone"`
			Language string          `json:"language"`
			Markdown string          `json:"markdown"`
			Outline  db.RecapOutline `json:"outline"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if decodeAIJSON(w, r, &body) != nil || len(body.Language) > 16 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_day"})
			return
		}
		zone, err := time.LoadLocation(firstNonEmpty(body.TimeZone, "UTC"))
		day, dateErr := time.ParseInLocation("2006-01-02", body.Date, zone)
		if err != nil || dateErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_day"})
			return
		}
		space, err := requestMemorySpace(r.Context(), s.database, user, body.SpaceID)
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		if body.Markdown != "" {
			id, err := s.database.SaveMemoryRecap(r.Context(), user, space, day, body.Markdown, body.Outline)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"id": id})
			return
		}
		events, err := s.database.MemoryEvents(r.Context(), user, db.MemoryQuery{SpaceID: space, From: day, To: day.AddDate(0, 0, 1), Limit: 200})
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if len(events) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"markdown": "", "topics": []string{}, "count": 0})
			return
		}
		// The desktop writes the summary on its own model (docs/agents/runtime.md).
		writeJSON(w, http.StatusOK, map[string]any{"on_device": true, "date": body.Date, "language": body.Language, "items": dayItems(events, zone), "count": len(events)})
	}
}

// dayItem is one moment of the day, as the desktop's day summary reads it.
type dayItem struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Site    string `json:"site,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

// dayItems is one day's moments, oldest first so they read as the day went.
func dayItems(events []db.MemoryEvent, zone *time.Location) []dayItem {
	items := make([]dayItem, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		items = append(items, dayItem{At: event.OccurredAt.In(zone).Format("15:04"), Kind: event.Kind, Title: truncateAgentRuntimeText(firstNonEmpty(event.Title, event.URL), 300), Site: event.Site, Excerpt: truncateAgentRuntimeText(event.Body, 400)})
	}
	return items
}
