package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var aiRecapSurfaceIDs = map[string]bool{"global": true, "home": true, "activity": true}

func (s *AIService) Recaps() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		items, err := s.database.AIRecaps(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"recaps": items})
	}
}

func (s *AIService) Recap() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		surfaceID := strings.TrimSpace(chi.URLParam(r, "surfaceID"))
		if !aiRecapSurfaceIDs[surfaceID] {
			http.Error(w, "invalid recap surface", http.StatusBadRequest)
			return
		}
		var body struct {
			Enabled   bool   `json:"enabled"`
			Cadence   string `json:"cadence"`
			LocalTime string `json:"local_time"`
			Weekday   int    `json:"weekday"`
			Timezone  string `json:"timezone"`
			Prompt    string `json:"prompt"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpsertAIRecap(r.Context(), userID, db.AIRecap{
			SurfaceID: surfaceID, Enabled: body.Enabled, Cadence: strings.TrimSpace(body.Cadence),
			LocalTime: strings.TrimSpace(body.LocalTime), Weekday: body.Weekday,
			Timezone: strings.TrimSpace(body.Timezone), Prompt: strings.TrimSpace(body.Prompt),
		}, time.Now().UTC())
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"recap": item})
	}
}

func (s *AIService) RecapSeen() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		surfaceID := strings.TrimSpace(chi.URLParam(r, "surfaceID"))
		if !aiRecapSurfaceIDs[surfaceID] {
			http.Error(w, "invalid recap surface", http.StatusBadRequest)
			return
		}
		if err := s.database.MarkAIRecapSeen(r.Context(), userID, surfaceID); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ProcessDueAIRecaps executes personal, explicitly enabled briefings. It uses
// recent permission-filtered records and also records a normal AI invocation,
// so schedules do not become an unmetered or invisible model path.
func (s *AIService) ProcessDueAIRecaps(ctx context.Context, now time.Time, limit int) (int, error) {
	items, err := s.database.ClaimDueAIRecaps(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, item := range items {
		if err := s.processAIRecap(ctx, item, now); err != nil {
			continue
		}
		completed++
	}
	return completed, nil
}

func (s *AIService) processAIRecap(ctx context.Context, item db.AIRecap, now time.Time) error {
	available, err := s.database.AIActionAvailable(ctx, item.UserID, item.SurfaceID, "recap", agent.InitialSelectedModelID)
	if err != nil || !available {
		if err == nil {
			err = errors.New("recap feature is unavailable")
		}
		_ = s.database.CompleteAIRecap(ctx, item, "", "", nil, err, now)
		return err
	}
	if !s.agentRuntime.Enabled() {
		err = errors.New("agent runtime is unavailable")
		_ = s.database.CompleteAIRecap(ctx, item, "", "", nil, err, now)
		return err
	}
	prompt := firstAIText(item.Prompt, "Summarize recent progress, upcoming commitments, decisions, risks, and blockers. Be concise and omit sections with no grounded evidence.")
	body := aiInvocationInput{Mode: "drawer", SurfaceID: item.SurfaceID, Trigger: "schedule", Prompt: prompt, Timezone: item.Timezone}
	payload, _ := json.Marshal(body)
	scheduledAt := now
	if item.NextRunAt != nil {
		scheduledAt = *item.NextRunAt
	}
	invocation, created, err := s.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_" + uuid.NewString(), UserID: item.UserID, SurfaceID: item.SurfaceID,
		Mode: "drawer", Trigger: "schedule", State: "queued",
		IdempotencyKey: "recap:" + item.SurfaceID + ":" + scheduledAt.UTC().Format(time.RFC3339),
		RequestPayload: payload, ExpiresAt: now.Add(aiInvocationTTL),
	})
	if err != nil {
		_ = s.database.CompleteAIRecap(ctx, item, "", "", nil, err, now)
		return err
	}
	if _, err := s.invocations.restoreDurable(ctx, invocation); err != nil {
		_ = s.database.CompleteAIRecap(ctx, item, "", "", nil, err, now)
		return err
	}
	if !created || aiInvocationTerminal(invocation.State) || invocation.RuntimeRunID != "" {
		return nil
	}
	if _, err := s.agentRuntime.Start(ctx, invocation.ID); err != nil {
		s.invocations.fail(invocation.ID, "Misty could not start the agent runtime. Please try again.")
		_ = s.database.CompleteAIRecap(ctx, item, invocation.ID, "", nil, err, now)
		return err
	}
	return nil
}
