package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/aimodels"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func isAIInvocationRuntimeID(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "invocation_")
}

func (s *SpacesService) agentRuntimeActivateAIInvocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RuntimeRunID string `json:"runtime_run_id"`
		RuntimeKind  string `json:"runtime_kind"`
	}
	if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
		return
	}
	record, err := s.database.ActivateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeKind, body.RuntimeRunID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if s.aiInvocations != nil {
		if _, err := s.aiInvocations.restoreDurable(r.Context(), *record); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load invocation stream"})
			return
		}
		s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "invocation.started", State: "running"})
		s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "assistant.status", Phase: "thinking"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "state": record.State})
}

func (s *SpacesService) agentRuntimeContextAIInvocation(w http.ResponseWriter, r *http.Request) {
	var body agentRuntimeIdentity
	if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	prepared, err := s.prepareAIInvocationRuntime(r.Context(), record)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if s.aiInvocations != nil {
		for _, item := range prepared.resolved {
			citation := item.Citation
			s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "citation", Citation: &citation})
		}
		if citation := aiSelectionCitation(prepared.body); citation != nil {
			s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "citation", Citation: citation})
		}
	}
	_ = s.database.TouchAIInvocationRuntime(r.Context(), record.ID, body.RuntimeRunID)
	attachments, err := s.aiInvocationModelAttachments(r.Context(), record)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	routes, err := s.database.FreezeAIModelRoutes(r.Context(), record.UserID, record.ID, defaultAIRoutes(prepared.modelID, prepared.reasoning), nil)
	if err != nil {
		writeAIProviderError(w, err)
		return
	}
	agentRoute, visionRoute := modelRoute(routes, "agent"), modelRoute(routes, "vision")
	if !agentRoute.Enabled {
		writeAIProviderError(w, aimodels.ErrDisabled)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": record.ID, "agent_id": prepared.body.AgentID, "space_id": prepared.spaceID,
		"space_name": prepared.spaceName, "space_kind": prepared.spaceKind,
		"timezone": prepared.timezone, "current_time": prepared.currentTime.Format(time.RFC3339),
		"members": prepared.members, "model_id": agentRoute.Model, "reasoning_effort": agentRoute.Reasoning, "vision_model_id": visionRoute.Model,
		"run_mode": "full", "system": prepared.system, "prompt": prepared.prompt,
		"attached_sources": []any{}, "file_warnings": "", "allowed_tools": prepared.allowedTools,
		"model_turn_limit":      record.ModelTurnLimit,
		"context_window_tokens": serveragent.ModelContextWindow(r.Context(), agentRoute.Model),
		"capture":               prepared.body.Capture,
		"display_captures":      prepared.body.DisplayCaptures,
		"companion_mode":        prepared.body.CompanionMode,
		"companion_explanation": false,
		"attachments":           attachments,
	})
}

func (s *SpacesService) agentRuntimeToolAIInvocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceHookToken string          `json:"device_hook_token"`
		RuntimeRunID    string          `json:"runtime_run_id"`
		CallID          string          `json:"call_id"`
		Name            string          `json:"name"`
		Arguments       json.RawMessage `json:"arguments"`
	}
	if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
		return
	}
	if strings.TrimSpace(body.CallID) == "" || len(body.Arguments) == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "tool_denied"})
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	prepared, err := s.prepareAIInvocationRuntime(r.Context(), record)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	// The legacy endpoint executes exactly like the MCP route.
	access := &mcpRuntimeAccess{record: record, prepared: prepared, claims: mcpAccessClaims{RuntimeRunID: body.RuntimeRunID}}
	result, err := s.executeAIInvocationMCPTool(r.Context(), access, agentRuntimeToolCall{RuntimeRunID: body.RuntimeRunID, CallID: body.CallID, Name: body.Name, Arguments: body.Arguments, DeviceHookToken: body.DeviceHookToken})
	var intervention *aiInterventionRequired
	switch {
	case errors.As(err, &intervention):
		writeJSON(w, http.StatusAccepted, map[string]any{"intervention_wait": intervention.wait})
	case errors.Is(err, errAIInvocationDeviceWait):
		writeJSON(w, http.StatusAccepted, map[string]any{"device_wait": true})
	case errors.Is(err, db.ErrSpaceForbidden), errors.Is(err, workflowv2.ErrCapabilityDenied):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "tool_denied"})
	case err != nil:
		writeAgentError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"result": json.RawMessage(result)})
	}
}

func runtimeToolStatus(name string) string {
	if name == "misty_finish_task" || name == "misty.finish.task" {
		return "Finishing task…"
	}
	label := strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
	label = strings.ReplaceAll(label, ".", " ")
	if label == "" {
		return "Checking Misty…"
	}
	return "Using " + label + "…"
}
