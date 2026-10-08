package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

// The runtime sends no "running" checkpoint for a read-only call
// (docs/plans/agentic-browser.md, Phase 2: fewer checkpoints). The call is
// announced here when it arrives instead, which saves the runtime a durable
// step and a round trip per read. Writes keep their own start checkpoint.
//
// Announcing records what the checkpoint would have: the conversation's
// "tool.started" and status events, and for agent runs the running step.
// Receipts match the checkpoint's own, so a runtime that still sends one does
// not show the call twice. Announcing never blocks the call itself.

func announcesToolStart(descriptor agenttools.Descriptor) bool {
	return descriptor.Risk == serveragent.RiskRead
}

func (s *SpacesService) announceAIInvocationToolStart(ctx context.Context, record *db.AIInvocationRecord, call agentRuntimeToolCall) {
	if s.aiInvocations == nil || record == nil || aiInvocationTerminal(record.State) {
		return
	}
	nodeID := "tool:" + call.CallID
	events := []aiInvocationEvent{
		{Type: "assistant.status", Phase: "tool", Text: runtimeToolStatus(call.Name)},
		{Type: "tool.started", ToolCallID: call.CallID, ToolName: call.Name},
	}
	for _, event := range events {
		receipt := call.RuntimeRunID + ":" + nodeID + ":running:" + event.Type
		err := s.aiInvocations.appendReceipt(record.ID, event, receipt)
		if errors.Is(err, db.ErrSpaceNotFound) {
			// The stream is not loaded on this replica yet.
			if _, restoreErr := s.aiInvocations.restoreDurable(ctx, *record); restoreErr != nil {
				return
			}
			err = s.aiInvocations.appendReceipt(record.ID, event, receipt)
		}
		if err != nil {
			log.Printf("announce %s for %s: %v", call.Name, record.ID, err)
			return
		}
	}
}

func (s *SpacesService) announcePersonalAgentToolStart(ctx context.Context, run *db.SpaceRun, call agentRuntimeToolCall) {
	if run == nil {
		return
	}
	nodeID := "tool:" + call.CallID
	phase := "using_" + strings.ReplaceAll(call.Name, ".", "_")
	input := json.RawMessage(`{}`)
	if validJSONObject(call.Arguments) {
		input = sanitizeAgentLifecycleJSON(call.Arguments)
	}
	if err := s.database.CheckpointWorkflowStep(ctx, run.ID, workflowv2.StepEvent{NodeID: nodeID, State: workflowv2.StepRunning, Attempt: 1, Input: input, Output: json.RawMessage(`{}`)}); err != nil {
		log.Printf("announce %s for %s: %v", call.Name, run.ID, err)
		return
	}
	if err := s.database.TouchPersonalAgentTaskRuntime(ctx, run.ID, call.RuntimeRunID, phase, 40); err != nil {
		log.Printf("announce %s for %s: %v", call.Name, run.ID, err)
	}
	s.projectLinkedAIInvocationEvent(ctx, run, nodeID, string(workflowv2.StepRunning), phase, nil)
}

// recordAIInvocationToolActivity adds what a finished call targeted and got
// back to the reply's timeline. It is best effort, like announcing the start.
func (s *SpacesService) recordAIInvocationToolActivity(record *db.AIInvocationRecord, call agentRuntimeToolCall, result json.RawMessage) {
	if s.aiInvocations == nil || record == nil || aiInvocationTerminal(record.State) {
		return
	}
	activity := toolActivity(call.Arguments, result)
	if activity == nil {
		return
	}
	event := aiInvocationEvent{Type: "tool.detail", ToolCallID: call.CallID, ToolName: call.Name, Activity: activity}
	_ = s.aiInvocations.appendReceipt(record.ID, event, call.RuntimeRunID+":tool:"+call.CallID+":detail")
}
