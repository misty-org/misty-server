package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func (s *SpacesService) agentRuntimeEventAIInvocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RuntimeRunID string          `json:"runtime_run_id"`
		NodeID       string          `json:"node_id"`
		State        string          `json:"state"`
		Phase        string          `json:"phase"`
		Output       json.RawMessage `json:"output"`
		ErrorMessage string          `json:"error_message"`
	}
	if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		// A model may finish while Stop is being committed. Acknowledge its signed
		// completion; never restore task/device authority.
		if strings.HasPrefix(body.NodeID, "model:") && body.State == "completed" {
			if stopped, lookupErr := s.database.AIInvocationRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID); lookupErr == nil && aiInvocationTerminal(stopped.State) {
				writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
				return
			}
		}
		writeAgentError(w, err)
		return
	}
	if body.State != "running" && body.State != "completed" && body.State != "failed" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_event_state"})
		return
	}
	if strings.HasPrefix(body.NodeID, "model:") {
		if err := s.admitAIInvocationRuntimeTurn(r.Context(), record, body.NodeID, body.State); err != nil {
			writeAgentError(w, err)
			return
		}
	}
	var eventErr error
	appendEvent := func(event aiInvocationEvent) {
		if eventErr != nil {
			return
		}
		receipt := body.RuntimeRunID + ":" + body.NodeID + ":" + body.State + ":" + event.Type
		eventErr = s.aiInvocations.appendReceipt(record.ID, event, receipt)
	}
	if s.aiInvocations != nil {
		if db.CompactionNode(body.NodeID) && body.State == "running" {
			appendEvent(compactionStatusEvent())
		}
		if strings.HasPrefix(body.NodeID, "tool:") {
			toolName := runtimeCheckpointToolName(body.Phase, body.Output)
			eventType := "tool.completed"
			if body.State == "running" {
				eventType = "tool.started"
				appendEvent(aiInvocationEvent{Type: "assistant.status", Phase: "tool", Text: runtimeToolStatus(toolName)})
			} else if body.State == "failed" {
				eventType = "tool.failed"
			}
			event := aiInvocationEvent{Type: eventType, ToolCallID: strings.TrimPrefix(body.NodeID, "tool:"), ToolName: toolName}
			if body.State != "running" {
				var timing struct {
					DurationMS int64 `json:"duration_ms"`
				}
				if json.Unmarshal(body.Output, &timing) == nil && timing.DurationMS > 0 {
					event.DurationMS = timing.DurationMS
				}
				if body.State == "failed" {
					event.Error = truncateAgentRuntimeText(strings.TrimSpace(body.ErrorMessage), 300)
				}
			}
			appendEvent(event)
		}
		if strings.HasPrefix(body.NodeID, "model:") && body.State == "completed" {
			var output struct {
				TextDelta string `json:"text_delta"`
			}
			if json.Unmarshal(body.Output, &output) == nil && strings.TrimSpace(output.TextDelta) != "" {
				appendEvent(aiInvocationEvent{Type: "response.delta", Delta: output.TextDelta})
			}
		}
	}
	if eventErr != nil {
		writeAgentError(w, eventErr)
		return
	}
	if err := s.database.TouchAIInvocationRuntime(r.Context(), record.ID, body.RuntimeRunID); err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

// Terminal checkpoints change phase to working/tool_failed; output retains the
// canonical tool identity. Do not expose raw tool results in the event stream.
func runtimeCheckpointToolName(phase string, output json.RawMessage) string {
	var metadata struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(output, &metadata) == nil && strings.TrimSpace(metadata.Tool) != "" {
		return metadata.Tool
	}
	return strings.ReplaceAll(strings.TrimPrefix(phase, "using_"), "_", ".")
}
