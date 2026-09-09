package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) RoutineManualRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		if envconfig.Getenv("MISTY_ROUTINES_ENABLED") != "true" || !sdkExecutionEnabled() || !s.agentRuntime.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "routine_execution_unavailable"})
			return
		}
		var body struct {
			RequestID string          `json:"requestId"`
			Version   int             `json:"version"`
			Trigger   json.RawMessage `json:"trigger"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		if len(body.Trigger) == 0 {
			body.Trigger = json.RawMessage(`{}`)
		}
		options := db.RoutineAdmissionOptions{TimedWaits: envconfig.Getenv("MISTY_ROUTINE_WAITS_ENABLED") == "true"}
		if envconfig.Getenv("MISTY_ROUTINE_AGENTS_ENABLED") == "true" {
			options.AgentModelID = serveragent.FrontierDefaultModelID()
		}
		run, err := s.database.AdmitManualRoutine(r.Context(), userID, chi.URLParam(r, "routineID"), body.RequestID, body.Version, body.Trigger, options)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, map[string]any{"runId": run.Execution.RunID, "requestId": run.RequestID, "state": run.State})
	}
}
func (s *SpacesService) RoutineRunControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		runID := chi.URLParam(r, "runID")
		if len(runID) > 256 {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		run, err := s.database.RoutineRun(r.Context(), userID, runID)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		if r.Method == http.MethodPost {
			if err := s.database.RequestRoutineCancellation(r.Context(), userID, runID); err != nil {
				writeSDKError(w, err)
				return
			}
			run, err = s.database.RoutineRun(r.Context(), userID, runID)
			if err != nil {
				writeSDKError(w, err)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, run)
	}
}
