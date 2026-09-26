package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/agenttools"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
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
	if prepared.sdkRequest != nil {
		writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "allowed_tools": prepared.allowedTools, "prompt": prepared.prompt, "sdk_execution": map[string]any{"name": prepared.allowedTools[0], "call_id": prepared.sdkRequest.EffectID, "input": prepared.sdkRequest.Request.Input}})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": record.ID, "agent_id": prepared.body.AgentID, "space_id": prepared.spaceID,
		"space_name": prepared.spaceName, "space_kind": prepared.spaceKind,
		"timezone": prepared.timezone, "current_time": prepared.currentTime.Format(time.RFC3339),
		"members": prepared.members, "model_id": prepared.modelID, "reasoning_effort": prepared.reasoning,
		"run_mode": "ask", "system": prepared.system, "prompt": prepared.prompt,
		"attached_sources": []any{}, "file_warnings": "", "allowed_tools": prepared.allowedTools,
		"required_tools":   prepared.requiredTools,
		"model_turn_limit": record.ModelTurnLimit,
		"capture":          prepared.body.Capture,
		"display_captures": prepared.body.DisplayCaptures,
		"companion_mode":   prepared.body.CompanionMode,
		"attachments":      attachments,
	})
}

func (s *SpacesService) agentRuntimeToolAIInvocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ApprovalHookToken string          `json:"approval_hook_token"`
		DeviceHookToken   string          `json:"device_hook_token"`
		RuntimeRunID      string          `json:"runtime_run_id"`
		CallID            string          `json:"call_id"`
		Name              string          `json:"name"`
		Arguments         json.RawMessage `json:"arguments"`
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
	if record.SurfaceID == "routine" {
		writeAgentError(w, db.ErrSpaceInvalid)
		return
	}
	if body.Name != toolboxWeatherCurrent && !agentToolNameAllowed(prepared.allowedTools, body.Name) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "tool_denied"})
		return
	}
	permitted := false
	for _, descriptor := range aiInvocationMCPDescriptors(prepared.allowedTools) {
		if descriptor.Name == body.Name {
			permitted, err = authorizeAppRuntimeTool(r.Context(), s.database, agenttools.Invocation{RunID: record.ID, UserID: record.UserID, SpaceID: prepared.spaceID}, descriptor)
			break
		}
	}
	if err != nil || !permitted {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "tool_denied"})
		return
	}
	if strings.HasPrefix(body.Name, "browser.") {
		access := &mcpRuntimeAccess{record: record, prepared: prepared, claims: mcpAccessClaims{RuntimeRunID: body.RuntimeRunID}}
		result, err := s.executeAIInvocationMCPTool(r.Context(), access, agentRuntimeToolCall{RuntimeRunID: body.RuntimeRunID, CallID: body.CallID, Name: body.Name, Arguments: body.Arguments, ApprovalHookToken: body.ApprovalHookToken, DeviceHookToken: body.DeviceHookToken})
		var intervention *aiInterventionRequired
		if errors.As(err, &intervention) {
			writeJSON(w, http.StatusAccepted, map[string]any{"intervention_wait": intervention.wait})
			return
		}
		if errors.Is(err, errAIInvocationDeviceWait) {
			writeJSON(w, http.StatusAccepted, map[string]any{"device_wait": true})
			return
		}
		var wait *browserApprovalRequired
		if errors.As(err, &wait) {
			writeJSON(w, http.StatusAccepted, map[string]any{"approval": wait.approval})
			return
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": json.RawMessage(result)})
		return
	}
	var result json.RawMessage
	r = r.WithContext(withAgentExecutionRuntime(r.Context(), body.RuntimeRunID))
	if prepared.spaceID == "" || body.Name == toolboxWeatherCurrent {
		bounded, cancel, budgetErr := boundedAgentExecutionContext(r.Context(), s.database, record.UserID, record.ID)
		if budgetErr != nil {
			writeAgentError(w, budgetErr)
			return
		}
		defer cancel()
		r = r.WithContext(bounded)
	}
	if body.Name == toolboxWeatherCurrent {
		var input struct {
			Location string `json:"location"`
		}
		if json.Unmarshal(body.Arguments, &input) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_weather_request"})
			return
		}
		result, err = currentWeather(r.Context(), input.Location)
	} else if body.Name == toolboxContextGet && prepared.spaceID == "" {
		result = TestingMustAPIRawJSON(map[string]any{
			"timezone": prepared.timezone, "current_time": prepared.currentTime.Format(time.RFC3339),
			"current_date": prepared.currentTime.Format("2006-01-02"), "scope": "account",
		})
	} else if prepared.spaceID == "" && (body.Name == toolboxMemoryRemember || body.Name == toolboxMemoryForget) {
		result, _, err = executeAgentMemoryTool(r.Context(), s.database, spaceConversationToolActor{
			userID: record.UserID, agentID: prepared.body.AgentID, runID: record.ID, sessionID: record.ConversationID,
		}, prepared.body.Prompt, serveragent.ToolRequest{ID: body.CallID, Name: body.Name, Arguments: body.Arguments})
	} else {
		actor := spaceConversationToolActor{
			userID: record.UserID, spaceID: prepared.spaceID, agentID: prepared.body.AgentID,
			runID: record.ID, sessionID: record.ConversationID,
		}
		toolbox, invocation, manifest, resolveErr := resolveAIInvocationSpaceToolbox(
			r.Context(), s.database, actor, prepared.body.Prompt,
			prepared.previousUserPrompt, prepared.previousAgentReply,
		)
		if resolveErr != nil || !agentManifestHasTool(manifest, body.Name) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "tool_denied"})
			return
		}
		result, err = executeSpaceAgentToolbox(r.Context(), toolbox, invocation, s.database, serveragent.ToolRequest{
			ID: body.CallID, Name: body.Name, Arguments: body.Arguments,
		})
	}
	if err != nil {
		if errors.Is(err, db.ErrSpaceForbidden) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "tool_denied"})
			return
		}
		writeAgentError(w, err)
		return
	}
	_ = s.database.TouchAIInvocationRuntime(r.Context(), record.ID, body.RuntimeRunID)
	writeJSON(w, http.StatusOK, map[string]any{"result": json.RawMessage(result)})
}

func runtimeToolStatus(name string) string {
	label := strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
	label = strings.ReplaceAll(label, ".", " ")
	if label == "" {
		return "Checking Misty…"
	}
	return "Using " + label + "…"
}
