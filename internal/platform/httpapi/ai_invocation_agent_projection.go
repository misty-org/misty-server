package api

import (
	"context"
	"encoding/json"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func linkedAIInvocationID(run *db.SpaceRun) string {
	if run == nil {
		return ""
	}
	var input struct {
		AIInvocationID string `json:"ai_invocation_id"`
	}
	if json.Unmarshal(run.Input, &input) != nil {
		return ""
	}
	return strings.TrimSpace(input.AIInvocationID)
}

func (s *SpacesService) restoreLinkedAIInvocation(ctx context.Context, run *db.SpaceRun) (*db.AIInvocationRecord, string) {
	id := linkedAIInvocationID(run)
	if id == "" || s.aiInvocations == nil || run == nil {
		return nil, ""
	}
	record, err := s.database.AIInvocationByID(ctx, run.OwnerUserID, id)
	if err != nil {
		return nil, ""
	}
	if _, err := s.aiInvocations.restoreDurable(ctx, *record); err != nil {
		return nil, ""
	}
	return record, id
}

func (s *SpacesService) projectLinkedAIInvocationStarted(ctx context.Context, run *db.SpaceRun) {
	if _, id := s.restoreLinkedAIInvocation(ctx, run); id != "" {
		s.aiInvocations.append(id, aiInvocationEvent{Type: "invocation.started", State: "running"})
		s.aiInvocations.append(id, aiInvocationEvent{Type: "assistant.status", Phase: "thinking"})
	}
}

func (s *SpacesService) projectLinkedAIInvocationEvent(ctx context.Context, run *db.SpaceRun, nodeID, state, phase string, output json.RawMessage) {
	if _, id := s.restoreLinkedAIInvocation(ctx, run); id == "" {
		return
	} else if strings.HasPrefix(nodeID, "tool:") {
		toolName := strings.TrimPrefix(phase, "using_")
		toolName = strings.ReplaceAll(toolName, "_", ".")
		eventType := "tool.completed"
		if state == "running" {
			eventType = "tool.started"
			s.aiInvocations.append(id, aiInvocationEvent{Type: "assistant.status", Phase: "tool", Text: runtimeToolStatus(toolName)})
		} else if state == "failed" {
			eventType = "tool.failed"
		}
		s.aiInvocations.append(id, aiInvocationEvent{Type: eventType, ToolCallID: strings.TrimPrefix(nodeID, "tool:"), ToolName: toolName})
	} else if strings.HasPrefix(nodeID, "model:") && state == "completed" {
		var value struct {
			TextDelta string `json:"text_delta"`
		}
		if json.Unmarshal(output, &value) == nil && strings.TrimSpace(value.TextDelta) != "" {
			s.aiInvocations.append(id, aiInvocationEvent{Type: "response.delta", Delta: value.TextDelta})
		}
	}
}

func (s *SpacesService) projectLinkedAIInvocationApproval(ctx context.Context, run *db.SpaceRun, toolName string) {
	if _, id := s.restoreLinkedAIInvocation(ctx, run); id != "" {
		s.aiInvocations.append(id, aiInvocationEvent{Type: "assistant.status", Phase: "approval", Text: "Waiting for your approval to use " + strings.ReplaceAll(toolName, ".", " ") + "."})
	}
}

func uniqueAgentToolNames(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (s *SpacesService) completeLinkedAIInvocation(ctx context.Context, run *db.SpaceRun, status, text, errorMessage string) error {
	record, id := s.restoreLinkedAIInvocation(ctx, run)
	if id == "" || record == nil || aiInvocationTerminal(record.State) {
		return nil
	}
	if status == "failed" {
		message := publicAgentRuntimeFailure("", errorMessage)
		s.aiInvocations.fail(id, message)
		return nil
	}
	prepared, err := s.prepareAIInvocationRuntime(ctx, record)
	if err != nil {
		return err
	}
	return s.finishAIInvocationRuntimeAnswer(record.UserID, id, prepared.body, strings.TrimSpace(text), prepared.resolved, prepared.prompt)
}
