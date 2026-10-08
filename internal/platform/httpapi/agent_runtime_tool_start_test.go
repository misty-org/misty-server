package api

import (
	"context"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestOnlyReadsAreAnnouncedOnArrival(t *testing.T) {
	if !announcesToolStart(agenttools.Descriptor{Name: "web.search", Risk: serveragent.RiskRead}) {
		t.Fatal("a read must be announced when it arrives")
	}
	for _, risk := range []string{serveragent.RiskWrite, serveragent.RiskDangerous} {
		if announcesToolStart(agenttools.Descriptor{Name: "notes.update", Risk: risk}) {
			t.Fatalf("a %s tool keeps its runtime start checkpoint", risk)
		}
	}
}

func TestAnnouncedReadShowsInTheConversation(t *testing.T) {
	service := &SpacesService{aiInvocations: newAIInvocationHub()}
	record := &db.AIInvocationRecord{ID: "invocation_announce", UserID: "user", State: "running"}
	call := agentRuntimeToolCall{RuntimeRunID: "wrun_1", CallID: "call_1", Name: "web.search"}
	// The stream is not loaded yet; announcing loads it.
	service.announceAIInvocationToolStart(context.Background(), record, call)
	stream := service.aiInvocations.invocations[record.ID]
	if stream == nil || len(stream.Events) != 2 {
		t.Fatalf("events = %#v", stream)
	}
	if stream.Events[0].Type != "assistant.status" || stream.Events[1].Type != "tool.started" ||
		stream.Events[1].ToolCallID != "call_1" || stream.Events[1].ToolName != "web.search" {
		t.Fatalf("events = %#v", stream.Events)
	}
	// A finished run shows nothing new.
	record.State = "completed"
	service.announceAIInvocationToolStart(context.Background(), record, agentRuntimeToolCall{RuntimeRunID: "wrun_1", CallID: "call_2", Name: "web.search"})
	if len(service.aiInvocations.invocations[record.ID].Events) != 2 {
		t.Fatal("a finished run must not be announced")
	}
}
