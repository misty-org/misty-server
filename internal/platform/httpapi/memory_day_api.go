package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

const daySummaryMaxOutput = 1200

// MemoryDaySummary summarizes one day of the person's Memory in one Space for
// their Journal, from that Space's moments only.
// POST {space_id, date: "2026-10-14", time_zone: "Europe/Lisbon", language}.
// The summary is returned, not stored; the person may save it as a Note.
func (s *SpacesService) MemoryDaySummary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		var body struct {
			SpaceID  string `json:"space_id"`
			Date     string `json:"date"`
			TimeZone string `json:"time_zone"`
			Language string `json:"language"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
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
		events, err := s.database.MemoryEvents(r.Context(), user, db.MemoryQuery{SpaceID: space, From: day, To: day.AddDate(0, 0, 1), Limit: 200})
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if len(events) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"markdown": "", "topics": []string{}, "count": 0})
			return
		}
		if s.models == nil || !s.models.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "day_summary_unavailable", "message": "Day summaries aren't available right now."})
			return
		}
		result, err := s.daySummary(r.Context(), user, body.Date, body.Language, zone, events)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"markdown": result.Markdown, "topics": result.Topics, "count": len(events)})
	}
}

func (s *SpacesService) daySummary(ctx context.Context, user, date, language string, zone *time.Location, events []db.MemoryEvent) (modelruntime.DaySummaryResult, error) {
	items := make([]modelruntime.DayItem, 0, len(events))
	size := 0
	// Oldest first reads as the day went.
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		excerpt := truncateAgentRuntimeText(event.Body, 400)
		items = append(items, modelruntime.DayItem{At: event.OccurredAt.In(zone).Format("15:04"), Kind: event.Kind, Title: truncateAgentRuntimeText(firstNonEmpty(event.Title, event.URL), 300), Site: event.Site, Excerpt: excerpt})
		size += len(excerpt) + len(event.Title) + 32
	}
	model := defaultRoleModel("day-summary")
	provider := aimodels.UsageProvider(model)
	key := "day-summary:" + uuid.NewString()
	var reservation *agent.UsageReservation
	if s.usageMeter != nil {
		var err error
		if reservation, err = agent.ReserveMeasuredUsage(s.usageMeter, user, key, provider, model, map[string]int64{"input_bytes": int64(size), "output_tokens": daySummaryMaxOutput}, ""); err != nil {
			return modelruntime.DaySummaryResult{}, err
		}
	}
	result, err := s.models.DaySummary(ctx, modelruntime.DaySummaryRequest{Route: modelruntime.Instance(), Model: model, Date: date, Items: items, Language: language, MaxOutputTokens: daySummaryMaxOutput})
	if settleErr := agent.FinishMeteredCall(s.usageMeter, reservation, key+":settle", "day_summary", provider, model, result.Usage, err); settleErr != nil && err == nil {
		return result, settleErr
	}
	return result, err
}
