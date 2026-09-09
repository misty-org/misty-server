package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// AgentRuntimeExecutionBudget is available only to the authenticated, pinned
// runtime. A budget response is a deadline, never a capability grant.
func (s *SpacesService) AgentRuntimeExecutionBudget() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RuntimeRunID string `json:"runtime_run_id"`
			Begin        bool   `json:"begin"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		runID := chi.URLParam(r, "runID")
		var userID string
		if isAIInvocationRuntimeID(runID) {
			record, err := s.database.ValidateAIInvocationRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			userID = record.UserID
		} else {
			run, _, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			userID = run.OwnerUserID
		}
		budget, err := s.database.AgentRunExecutionBudget(r.Context(), userID, runID, body.RuntimeRunID, body.Begin)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, budget)
	}
}

type agentExecutionRuntimeKey struct{}

func withAgentExecutionRuntime(ctx context.Context, runtimeID string) context.Context {
	return context.WithValue(ctx, agentExecutionRuntimeKey{}, runtimeID)
}

func (s *SpacesService) agentExecutionContext(ctx context.Context, userID, runID, runtimeID string) (context.Context, context.CancelFunc, error) {
	return boundedAgentExecutionContext(withAgentExecutionRuntime(ctx, runtimeID), s.database, userID, runID)
}

func boundedAgentExecutionContext(ctx context.Context, database *db.Database, userID, runID string) (context.Context, context.CancelFunc, error) {
	runtimeID, _ := ctx.Value(agentExecutionRuntimeKey{}).(string)
	// Legacy execution has no durable runtime identity and retains its old policy.
	if runtimeID == "" {
		return ctx, func() {}, nil
	}
	budget, err := database.AgentRunExecutionBudget(ctx, userID, runID, runtimeID, true)
	if err != nil {
		return ctx, func() {}, errors.Join(db.ErrAgentToolboxNotAttempted, err)
	}
	if budget.Version == 0 || budget.Deadline == nil {
		return ctx, func() {}, nil
	}
	bounded, cancel := context.WithDeadlineCause(ctx, *budget.Deadline, db.ErrAgentExecutionTimeLimit)
	return bounded, cancel, nil
}
