package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Following sub-agents: waiting for them, reporting their answers to the
// parent and its conversation, and stopping them with the parent.

func childFinished(state string) bool {
	return aiInvocationTerminal(state) || state == "missing"
}

// awaitChildren holds the call until the children finish or the hold ends,
// showing their progress in the parent's conversation as it changes.
func (s *SpacesService) awaitChildren(ctx context.Context, record *db.AIInvocationRecord, callID string) (json.RawMessage, error) {
	deadline := time.Now().Add(delegationHold)
	last := ""
	for {
		children, err := s.database.DelegationChildren(ctx, record.UserID, record.ID, callID)
		if err != nil {
			return nil, err
		}
		if signature := delegationSignature(children); signature != last {
			last = signature
			s.publishDelegation(ctx, record)
		}
		running := 0
		for _, child := range children {
			if !childFinished(child.State) {
				running++
			}
		}
		if running == 0 || time.Now().After(deadline) {
			return delegationResult(children, running), nil
		}
		select {
		case <-ctx.Done():
			return delegationResult(children, running), nil
		case <-time.After(2 * time.Second):
		}
	}
}

func delegationSignature(children []db.DelegationChild) string {
	parts := make([]string, 0, len(children))
	for _, child := range children {
		parts = append(parts, child.ChildInvocationID+":"+child.State)
	}
	return strings.Join(parts, ",")
}

func delegationResult(children []db.DelegationChild, running int) json.RawMessage {
	results := make([]map[string]any, 0, len(children))
	for _, child := range children {
		item := map[string]any{"task": child.TaskIndex + 1, "goal": child.Goal, "state": child.State}
		answer := strings.TrimSpace(child.Answer)
		if len(child.OutputSchema) > 0 && answer != "" {
			// A child asked for JSON answers with only that object.
			start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
			var parsed map[string]any
			if start >= 0 && end > start && json.Unmarshal([]byte(answer[start:end+1]), &parsed) == nil {
				item["output"] = parsed
			} else {
				item["answer"] = truncateAgentRuntimeText(answer, 6000)
			}
		} else if answer != "" {
			item["answer"] = truncateAgentRuntimeText(answer, 6000)
		}
		if child.State == "failed" && child.Error != "" {
			item["error"] = child.Error
		}
		results = append(results, item)
	}
	out := map[string]any{"subagents": results, "still_running": running, "contentTrust": "untrusted-subagent-output"}
	if running > 0 {
		out["next"] = "Some sub-agents are still working. Do other work or call agents_wait."
	}
	return TestingMustAPIRawJSON(out)
}

// publishDelegation shows the parent's sub-agents in its conversation.
func (s *SpacesService) publishDelegation(ctx context.Context, record *db.AIInvocationRecord) {
	if s.aiInvocations == nil {
		return
	}
	children, err := s.database.DelegationChildren(ctx, record.UserID, record.ID, "")
	if err != nil {
		return
	}
	event := &aiDelegationEvent{Children: make([]aiDelegatedChild, 0, len(children))}
	for _, child := range children {
		summary := ""
		if aiInvocationTerminal(child.State) {
			summary = aiConciseSummary(child.Answer)
		}
		event.Children = append(event.Children, aiDelegatedChild{InvocationID: child.ChildInvocationID, Goal: truncateAgentRuntimeText(child.Goal, 300), State: child.State, Summary: summary})
	}
	if _, err := s.aiInvocations.restoreDurable(ctx, *record); err != nil {
		return
	}
	_ = s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "delegation.updated", Delegation: event})
}

// cancelDelegatedChildren stops a parent's sub-agents that are still running:
// when the person stops the parent, and when the parent finishes without them.
func (s *SpacesService) cancelDelegatedChildren(ctx context.Context, user, parent string) {
	children, err := s.database.DelegationChildren(ctx, user, parent, "")
	if err != nil {
		return
	}
	for _, child := range children {
		if childFinished(child.State) {
			continue
		}
		record, err := s.database.AIInvocationByID(ctx, user, child.ChildInvocationID)
		if err != nil || aiInvocationTerminal(record.State) {
			continue
		}
		if record.RuntimeRunID != "" && s.agentRuntime.Enabled() {
			_ = s.agentRuntime.Cancel(ctx, record.RuntimeRunID, record.ID)
		}
		if s.aiInvocations != nil {
			if _, err := s.aiInvocations.restoreDurable(ctx, *record); err == nil {
				_ = s.aiInvocations.cancel(record.ID)
			}
		}
	}
}
