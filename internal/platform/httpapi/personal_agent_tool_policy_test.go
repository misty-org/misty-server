package api

import (
	"encoding/json"
	"testing"
)

func TestInheritInvokerEnablesActionsByDefault(t *testing.T) {
	policy := json.RawMessage(`{"mode":"inherit_invoker","disabled_surfaces":[]}`)
	if !TestingPersonalAgentCapabilityAllowed(policy, "tasks.update", "write") {
		t.Fatal("expected inherited Space access to allow task updates")
	}
	if !TestingPersonalAgentCapabilityAllowed(policy, "browser.navigate", "write") {
		t.Fatal("expected inherited Browser access to allow navigation")
	}
}

func TestInheritInvokerHonorsBroadSurfaceOptOut(t *testing.T) {
	policy := json.RawMessage(`{"mode":"inherit_invoker","disabled_surfaces":["browser","spaces"]}`)
	if TestingPersonalAgentCapabilityAllowed(policy, "browser.navigate", "write") {
		t.Fatal("expected Browser opt-out to deny navigation")
	}
	if TestingPersonalAgentCapabilityAllowed(policy, "tasks.query", "read") {
		t.Fatal("expected Spaces opt-out to deny task queries")
	}
}
