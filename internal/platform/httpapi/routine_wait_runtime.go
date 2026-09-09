package api

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

func (s *SpacesService) AgentRuntimeRoutineWait() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RuntimeRunID string `json:"runtime_run_id"`
			Phase        string `json:"phase"`
			StepID       string `json:"step_id"`
			WaitID       string `json:"wait_id,omitempty"`
			Until        string `json:"until,omitempty"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if record.SurfaceID != "routine" {
			writeAgentError(w, db.ErrSpaceForbidden)
			return
		}
		run, err := s.database.RoutineRun(r.Context(), record.UserID, record.ID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if run.CancelRequested || run.Outcome != "" {
			writeAgentError(w, db.ErrSpaceConflict)
			return
		}
		receipts, err := s.routineReceipts(r.Context(), record)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		plan, err := evaluateRoutinePlan(record, run, receipts, s.restoreAgentEffectResult)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		waitID := cap.RoutineWaitID(record.UserID, record.ID, body.StepID)
		expected, ok := plan.Replays[waitID]
		if plan.Next != nil && plan.Next.Wait && plan.Next.Binding.StepID == body.StepID {
			expected = *plan.Next
			ok = true
		}
		if !ok || !expected.Wait {
			writeAgentError(w, db.ErrSpaceConflict)
			return
		}
		// Recheck current provider/target access on each side of the durable wait.
		if _, err := s.prepareRoutineRuntime(r.Context(), record); err != nil {
			writeAgentError(w, err)
			return
		}
		var result *db.RoutineWaitRecord
		switch body.Phase {
		case "open":
			var resolved string
			if json.Unmarshal(expected.Input, &resolved) != nil || body.Until != resolved {
				writeAgentError(w, db.ErrSpaceConflict)
				return
			}
			until, parseErr := cap.RoutineWaitUntil(resolved)
			if parseErr != nil {
				writeAgentError(w, parseErr)
				return
			}
			result, err = s.database.OpenRoutineWait(r.Context(), record.UserID, record.ID, record.RuntimeRunID, body.StepID, until)
		case "resume":
			if body.WaitID != waitID {
				writeAgentError(w, db.ErrSpaceConflict)
				return
			}
			result, err = s.database.ResumeRoutineWait(r.Context(), record.UserID, record.ID, record.RuntimeRunID, body.StepID, body.WaitID)
		default:
			err = db.ErrSpaceInvalid
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	}
}
