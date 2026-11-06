package api

import (
	"context"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/agent"
	"github.com/misty-org/misty-server/internal/agenttools"
)

func TestRecallToolsGateThemselves(t *testing.T) {
	service := &SpacesService{}
	registrations := service.recallToolRegistrations()
	if len(registrations) != 4 {
		t.Fatalf("recall tools = %d", len(registrations))
	}
	for _, registration := range registrations {
		// They ask through grants, not the generic Ask pause.
		if gatedByAsk(registration.Descriptor) {
			t.Fatalf("%s goes through the generic gate", registration.Descriptor.Name)
		}
	}
	forget := registrations[3]
	if forget.Descriptor.Name != recallForgetTool || forget.Descriptor.Risk != agent.RiskWrite {
		t.Fatalf("forget = %+v", forget.Descriptor)
	}
	// Forgetting everything is for Settings, never an agent.
	if _, err := forget.Handler(context.Background(), agenttools.Invocation{UserID: "u"}, agent.ToolRequest{Name: recallForgetTool, Arguments: []byte(`{}`)}); err == nil {
		t.Fatal("forget without a site or range was accepted")
	}
}

func TestCursorCompanionMentionsMemoryOnlyWhenRecallIsOffered(t *testing.T) {
	with := companionSystemPrompt(aiInvocationInput{Mode: "companion"}, []string{screenLookTool, recallSearchTool})
	without := companionSystemPrompt(aiInvocationInput{Mode: "companion"}, []string{screenLookTool})
	if !strings.Contains(with, "memory_history_search") || strings.Contains(without, "memory_history_search") {
		t.Fatal("the cursor prompt should mention Memory only when memory.history.search is in the run")
	}
}
