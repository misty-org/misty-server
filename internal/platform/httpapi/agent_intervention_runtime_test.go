package api

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestInterventionRuntimeRequiresNegotiationAndAttachedReadGrant(t *testing.T) {
	service := &SpacesService{}
	_, err := service.executePersonalAgentRuntimeTool(t.Context(), &db.SpaceRun{ID: "run_test"}, agentRuntimeToolCall{CallID: "call", Name: "browser.request_user_action", Arguments: json.RawMessage(`{"scopeId":"original","action":"sign_in","reason":"Sign in"}`)})
	if !errors.Is(err, db.ErrSpaceForbidden) {
		t.Fatalf("unnegotiated runtime admitted a wait: %v", err)
	}
	grant := db.AgentDeviceGrant{Capabilities: json.RawMessage(`["browser.inspect"]`), ExpiresAt: time.Now().Add(time.Hour)}
	if !activeBrowserRuntimeCapability([]db.AgentDeviceGrant{grant}, "browser.request_user_action") {
		t.Fatal("attached read grant cannot request user help")
	}
	if activeBrowserCapability([]db.AgentDeviceGrant{grant}, "browser.request_user_action") {
		t.Fatal("legacy tool catalog gained a durable-only capability")
	}
	if companionToolNeedsApproval("ask", companionToolImpact("browser.request_user_action")) {
		t.Fatal("pausing for the user demanded another effect approval")
	}
	expired := time.Now().Add(-time.Minute)
	grant.RevokedAt = &expired
	if activeBrowserRuntimeCapability([]db.AgentDeviceGrant{grant}, "browser.request_user_action") {
		t.Fatal("revoked grant exposed a wait")
	}
}
func TestPendingSpaceInterventionCannotExecuteAnotherTool(t *testing.T) {
	service := &SpacesService{}
	for _, name := range []string{"browser.inspect", "messages.send", "sdk.example"} {
		result, err := service.callPersonalAgentMCPTool(t.Context(), &mcpRuntimeAccess{run: &db.SpaceRun{State: "awaiting_intervention"}}, agenttools.Descriptor{Name: name}, nil)
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("pending run admitted %s: %#v %v", name, result, err)
		}
	}
}
