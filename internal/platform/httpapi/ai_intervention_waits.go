package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type aiInterventionRequired struct{ wait *db.AIInterventionWait }

func (e *aiInterventionRequired) Error() string { return "user_intervention_required" }
func (s *SpacesService) requestAIUserAction(ctx context.Context, user, run string, call agentRuntimeToolCall) (json.RawMessage, error) {
	var input struct {
		ScopeID string `json:"scopeId"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal(call.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	var raw map[string]any
	if json.Unmarshal(call.Arguments, &raw) != nil || len(raw) != 3 {
		return nil, db.ErrSpaceInvalid
	}
	canonical, _ := json.Marshal(input)
	digest := sha256.Sum256(canonical)
	wait, err := s.database.AwaitAgentUserIntervention(ctx, user, run, call.RuntimeRunID, call.CallID, call.DeviceHookToken, input.ScopeID, input.Action, input.Reason, hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, err
	}
	if wait.State == "pending" {
		return nil, &aiInterventionRequired{wait}
	}
	if wait.State != "ready" {
		return TestingMustAPIRawJSON(map[string]any{"denied": true, "reason": "user_action_declined_or_expired", "waitId": wait.ID}), nil
	}
	return TestingMustAPIRawJSON(map[string]any{"ready": true, "waitId": wait.ID, "scopeId": wait.ScopeID, "requiresFreshInspection": true, "message": "The user confirmed readiness. Inspect the original target again; this is not verification of account identity or any browser effect."}), nil
}
func (s *SpacesService) AIUserInterventionControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet {
			waits, err := s.database.AIUserInterventions(r.Context(), user)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"waits": waits})
			return
		}
		var body struct {
			Ready *bool `json:"ready"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		if body.Ready == nil {
			writeAgentError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.DecideAIUserIntervention(r.Context(), user, chi.URLParam(r, "waitID"), *body.Ready); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
	}
}
