package api

import (
	"encoding/json"
	"github.com/kannachi323/misty/server/internal/capabilities"
	"testing"
)

func TestWorkspaceAutopilotBoundary(t *testing.T) {
	schema, err := capabilities.CompileSchema(workspaceInteractionSchema())
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal([]byte(`{"scopeId":"workspace-scope","documentId":"2d9dd46a-90b5-40b3-a72b-b7fb87c57c0d","consequential":false,"action":{"kind":"point","x":0.3,"y":0.6}}`), &value)
	if err = schema.Validate(value); err != nil {
		t.Fatal(err)
	}
	delete(value, "documentId")
	if schema.Validate(value) == nil {
		t.Fatal("missing observation accepted")
	}
	value["documentId"] = "2d9dd46a-90b5-40b3-a72b-b7fb87c57c0d"
	value["action"].(map[string]any)["x"] = 1.5
	if schema.Validate(value) == nil {
		t.Fatal("outside-window point accepted")
	}
	for _, mode := range []string{"user", "team"} {
		if nativeAgentToolAllowed("browser.workspace.visual", "read", mode, map[string]bool{"browser": true}) {
			t.Fatalf("window control exposed in %s", mode)
		}
	}
	body := aiInvocationInput{AgentID: "agent", ExecutionMode: "agent", WindowLabel: "main"}
	if !nativeRoutineBrowserAction(body, "browser.workspace.interact", json.RawMessage(`{"consequential":false,"action":{"kind":"point"}}`), "Page") {
		t.Fatal("routine foreground click requires extra review")
	}
	for _, raw := range []string{`{"consequential":true,"action":{"kind":"point"}}`, `{"action":{"kind":"point"}}`, `{"consequential":false,"action":{"kind":"key","key":"Enter"}}`} {
		if nativeRoutineBrowserAction(body, "browser.workspace.interact", json.RawMessage(raw), "Page") {
			t.Fatal("consequential or unknown action skipped review")
		}
	}
}
