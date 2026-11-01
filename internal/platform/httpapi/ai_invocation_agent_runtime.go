package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
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
	var body agentRuntimeContextRequest
	if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	prepared, err := s.prepareAIInvocationRuntime(r.Context(), record, body.deviceTools()...)
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
	modelID, reasoning := prepared.modelID, prepared.reasoning
	if device, effort, ok := body.deviceModel(); ok {
		// A desktop runtime thinks on the device's own models.
		modelID, reasoning = device, effort
	}
	routes, err := s.database.FreezeAIModelRoutes(r.Context(), record.UserID, record.ID, runModelRoutes(modelID, reasoning))
	if err != nil {
		writeAIProviderError(w, err)
		return
	}
	agentRoute, visionRoute := modelRoute(routes, "agent"), modelRoute(routes, "vision")
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": record.ID, "agent_id": prepared.body.AgentID, "space_id": prepared.spaceID,
		"space_name": prepared.spaceName, "space_kind": prepared.spaceKind,
		"timezone": prepared.timezone, "current_time": prepared.currentTime.Format(time.RFC3339),
		"members": prepared.members, "model_id": agentRoute.Model, "reasoning_effort": agentRoute.Reasoning, "vision_model_id": visionRoute.Model,
		"run_mode": prepared.body.PermissionMode, "system": prepared.system, "prompt": prepared.prompt,
		"attached_sources": []any{}, "file_warnings": "", "allowed_tools": prepared.allowedTools,
		"model_turn_limit":      record.ModelTurnLimit,
		"context_window_tokens": agent.ModelContextWindow(r.Context(), agentRoute.Model),
		"capture":               prepared.body.Capture,
		"display_captures":      prepared.body.DisplayCaptures,
		"companion_mode":        prepared.body.CompanionMode,
		"teach":                 teachTurn(prepared.body),
		"teach_tool_budget":     teachToolBudget(prepared.body),
		"attachments":           attachments,
		"browser_replay":        s.browserWorkflowReplayContext(r.Context(), record, prepared.body),
		// A desktop runtime names a new chat from its opening message as the run ends.
		"title_prompt": deviceTitlePrompt(r.Context(), s.database, record.UserID, record.ConversationID, prepared.body),
		// The older turns that aged out, for the desktop to write notes on as the run ends.
		"summary_request": s.deviceSummaryRequest(r.Context(), record, prepared.body, modelID),
	})
}

func runtimeToolStatus(name string) string {
	if name == "agent_finish" || name == "agent.finish" {
		return "Finishing task…"
	}
	label := strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
	label = strings.ReplaceAll(label, ".", " ")
	if label == "" {
		return "Checking Misty…"
	}
	return "Using " + label + "…"
}
