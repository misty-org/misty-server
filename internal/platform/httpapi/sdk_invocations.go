package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) InvokeSDKCapability() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !sdkExecutionEnabled() || !s.agentRuntime.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "sdk_execution_unavailable"})
			return
		}
		var request cap.Invocation
		if !decodeCapabilityRequest(w, r, &request) {
			return
		}
		record, err := s.database.AdmitSDKInvocation(r.Context(), userID, request)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"requestId": record.Request.RequestID, "runId": db.SDKPublicRunID(record.InvocationID)})
	}
}
func (s *SpacesService) SDKCapabilityResult() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		request, err := s.database.SDKInvocationByRequest(r.Context(), userID, chi.URLParam(r, "requestID"))
		if err != nil {
			writeSDKError(w, err)
			return
		}
		state := request.State
		var outcome json.RawMessage
		if state == "awaiting_intervention" {
			pending, pendingErr := s.database.SDKPendingBrowserIntervention(r.Context(), userID, request.InvocationID, "", "")
			if pendingErr != nil {
				writeSDKError(w, pendingErr)
				return
			}
			state = "waiting"
			if pending != nil {
				outcome, _ = json.Marshal(map[string]any{"status": "user_intervention_required", "waitId": pending.ID, "expiresAt": pending.ExpiresAt, "action": pending.Action, "reason": pending.Reason})
			}
		}
		if state == "awaiting_approval" {
			state = "waiting"
		}
		if state == "canceled" {
			state = "cancelled"
		}
		if request.OutcomeStatus == "uncertain" {
			state = "uncertain"
		}
		if len(outcome) == 0 && len(request.OutcomeCiphertext) > 0 && (state == "completed" || state == "failed" || state == "uncertain" || state == "cancelled" || state == "waiting") {
			outcome, err = s.restoreAgentEffectResult(request.EffectID+":outcome", request.OutcomeCiphertext)
			if err != nil {
				writeSDKError(w, err)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"requestId": request.Request.RequestID, "state": state, "outcome": outcome})
	}
}
func (s *SpacesService) CancelSDKCapability() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		request, err := s.database.SDKInvocationByRequest(r.Context(), userID, chi.URLParam(r, "requestID"))
		if err != nil {
			writeSDKError(w, err)
			return
		}
		if err := s.database.RequestSDKCancellation(r.Context(), userID, request.InvocationID); err != nil {
			writeSDKError(w, err)
			return
		}
		record, err := s.database.AIInvocationByID(r.Context(), userID, request.InvocationID)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		if record.State == "canceled" && record.RuntimeRunID == "" {
			if err := s.completeSDKInvocation(r.Context(), record, true); err != nil {
				writeSDKError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func (s *SpacesService) SDKCapabilityApproval() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		runID := chi.URLParam(r, "runID")
		approvalID := chi.URLParam(r, "approvalID")
		if !cap.ValidID(runID) || !cap.ValidID(approvalID) {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		var body struct {
			Approved *bool `json:"approved"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		if body.Approved == nil {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		if err := s.database.DecideSDKToolApproval(r.Context(), userID, "invocation_"+strings.ToLower(runID), approvalID, *body.Approved); err != nil {
			writeSDKError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
