package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// codingEvent is one event a device mirrors from its coding tool, in the
// runner vocabulary (src-tauri/src/agents/runners/events.rs).
type codingEvent struct {
	Seq        int64   `json:"seq"`
	Type       string  `json:"type"`
	Text       string  `json:"text"`
	Message    string  `json:"message"`
	StopReason string  `json:"stop_reason"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheWrite int64   `json:"cache_write"`
	CacheRead  int64   `json:"cache_read"`
	Model      string  `json:"model"`
	UsedPct    float64 `json:"used_percent"`
	Window     string  `json:"window"`
	Items      []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"items"`
}

// CodingEvents records what a delegated coding run's tool did, so the chat's
// stream, replay, progress and checklist work as for Misty's own runs:
// POST /me/coding/{invocationID}/events {device_id, lease_id, token, events}.
// Each event is stored once by its sequence number.
func (s *SpacesService) CodingEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !requireCoding(w, r, s.database, user, "coding.events") {
			return
		}
		var body struct {
			DeviceID string        `json:"device_id"`
			LeaseID  string        `json:"lease_id"`
			Token    string        `json:"token"`
			Events   []codingEvent `json:"events"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		run, err := s.codingRun(r.Context(), user, chi.URLParam(r, "invocationID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if body.LeaseID == "" {
			// Without a lease the device can only report that the run stopped.
			if len(body.Events) != 1 || body.Events[0].Type != "error" {
				writeSpaceError(w, db.ErrSpaceForbidden)
				return
			}
			if err := s.settleCodingLeases(r.Context(), run, ""); err != nil {
				writeBillingError(w, err)
				return
			}
			_ = s.aiInvocations.fail(run.record.ID, strings.TrimSpace(firstAIText(body.Events[0].Message, "The coding tool couldn't start.")))
			writeJSON(w, http.StatusOK, map[string]any{"accepted": 1})
			return
		}
		lease, err := s.database.CodingLeaseByToken(r.Context(), user, run.record.ID, body.LeaseID, body.Token)
		if err != nil || len(body.Events) > 200 {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		for _, event := range body.Events {
			if err := s.recordCodingEvent(r.Context(), run, lease, event); err != nil {
				writeSpaceError(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"accepted": len(body.Events)})
	}
}

func (s *SpacesService) recordCodingEvent(ctx context.Context, run *codingRun, lease *db.CodingLease, event codingEvent) error {
	receipt := "coding:" + run.record.ID + ":" + strconv.FormatInt(event.Seq, 10)
	record := func(item aiInvocationEvent) error { return s.aiInvocations.appendReceipt(run.record.ID, item, receipt) }
	switch event.Type {
	case "delta":
		return record(aiInvocationEvent{Type: "response.delta", Delta: event.Text})
	case "message":
		return record(aiInvocationEvent{Type: "assistant.message", Text: event.Text, Summary: aiConciseSummary(event.Text)})
	case "todos":
		items := make([]db.ChecklistItem, 0, len(event.Items))
		for _, item := range event.Items {
			items = append(items, db.ChecklistItem{ID: item.ID, Title: item.Title, Status: item.Status})
		}
		if len(items) == 0 {
			return nil
		}
		// The checklist is shown when the run has a chat to show it in.
		var invalid agent.ErrInvalidRequest
		if _, err := s.publishChecklist(ctx, run.record, collaborationState{mode: db.ConversationModeAct}, items, true); err != nil && !errors.As(err, &invalid) {
			return err
		}
		return nil
	case "usage":
		usage := codingUsage{Input: event.Input, Output: event.Output, CacheWrite: event.CacheWrite, CacheRead: event.CacheRead}
		return s.database.AddCodingLeaseUsage(ctx, run.record.UserID, lease.ID, db.CodingLeaseUsage{
			Input: usage.Input, Output: usage.Output, CacheWrite: usage.CacheWrite, CacheRead: usage.CacheRead, Weighted: usage.weighted(codingExternalFactor()),
		})
	case "rate_limit":
		limit, _ := json.Marshal(map[string]any{"used_percent": event.UsedPct, "window": event.Window})
		return s.database.SaveCodingEngines(ctx, run.record.UserID, lease.DeviceID, []db.CodingEngine{{Engine: lease.Engine, Installed: true, SignedIn: true, RateLimit: limit}})
	case "error":
		if err := s.settleCodingLeases(ctx, run, ""); err != nil {
			return err
		}
		return s.aiInvocations.fail(run.record.ID, strings.TrimSpace(firstAIText(event.Message, "The coding tool stopped with an error.")))
	case "done":
		if err := s.settleCodingLeases(ctx, run, ""); err != nil {
			return err
		}
		return s.aiInvocations.complete(run.record.ID)
	}
	// Sessions, thinking and permission requests are handled on the device.
	return nil
}

// settleCodingLeases tells billing what each of the run's finished leases
// used, once per lease; keep names a lease still in use. Settling releases
// whatever the lease reserved and didn't use.
func (s *SpacesService) settleCodingLeases(ctx context.Context, run *codingRun, keep string) error {
	leases, err := s.database.UnsettledCodingLeases(ctx, run.record.UserID, run.record.ID)
	if err != nil {
		return err
	}
	meter, _ := s.usageMeter.(interface {
		CompleteRuntimeTurn(context.Context, string, string, string, agent.ModelUsage) error
	})
	factor := codingExternalFactor()
	for _, lease := range leases {
		if lease.ID == keep {
			continue
		}
		if meter != nil {
			usage := codingUsage{Input: lease.Usage.Input, Output: lease.Usage.Output, CacheWrite: lease.Usage.CacheWrite, CacheRead: lease.Usage.CacheRead}
			if err := meter.CompleteRuntimeTurn(ctx, run.record.UserID, run.record.ID, lease.ID, usage.billed(factor)); err != nil {
				return err
			}
		}
		if err := s.database.MarkCodingLeaseSettled(ctx, run.record.UserID, lease.ID); err != nil {
			return err
		}
	}
	return nil
}
