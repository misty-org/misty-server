package agent

import (
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestPermissionPolicyModes(t *testing.T) {
	policy := PermissionPolicy{}
	requests := []ToolRequest{
		{Name: ToolListDirectory, Risk: RiskRead},
		{Name: ToolApplyFilePlan, Risk: RiskWrite},
		{Name: "shell", Risk: RiskDangerous},
	}

	ask := policy.Apply(ModeAsk, cloneRequests(requests))
	if !ask[0].ApprovalRequired || !ask[1].ApprovalRequired || !ask[2].ApprovalRequired {
		t.Fatalf("ask mode should require all approvals: %#v", ask)
	}

	auto := policy.Apply(ModeAuto, cloneRequests(requests))
	if auto[0].ApprovalRequired || auto[1].ApprovalRequired || !auto[2].ApprovalRequired {
		t.Fatalf("auto mode approval mismatch: %#v", auto)
	}

	full := policy.Apply(ModeFull, cloneRequests(requests))
	if full[0].ApprovalRequired || full[1].ApprovalRequired || full[2].ApprovalRequired {
		t.Fatalf("full mode should not require approvals: %#v", full)
	}
}

func TestToolRequestsAreBoundToManifestAndManifestRisk(t *testing.T) {
	requests, rejected := TestingAuthorizeToolRequests(ToolManifest{Tools: []ToolDefinition{{Name: "provider.send_message", Risk: RiskDangerous}}}, []ToolRequest{
		{ID: "allowed", Name: "provider.send_message", Risk: RiskRead, Arguments: json.RawMessage(`{"channel":"C1"}`)},
		{ID: "unknown", Name: "provider.delete_all", Risk: RiskRead, Arguments: json.RawMessage(`{}`)},
		{ID: "malformed", Name: "provider.send_message", Risk: RiskRead, Arguments: json.RawMessage(`[]`)},
	})
	if rejected != 2 || len(requests) != 1 || requests[0].Risk != RiskDangerous {
		t.Fatalf("requests=%#v rejected=%d", requests, rejected)
	}
}

func cloneRequests(requests []ToolRequest) []ToolRequest {
	return append([]ToolRequest(nil), requests...)
}
