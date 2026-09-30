package api

import (
	"encoding/json"
	"testing"
)

func TestRuntimeCheckpointPreservesToolIdentityAcrossPhases(t *testing.T) {
	for _, phase := range []string{"working", "tool_failed"} {
		if got := runtimeCheckpointToolName(phase, json.RawMessage(`{"tool":"browser.inspect","success":false}`)); got != "browser.inspect" {
			t.Fatalf("%s projected %q", phase, got)
		}
	}
	if got := runtimeCheckpointToolName("using_browser_inspect", nil); got != "browser.inspect" {
		t.Fatalf("start/legacy event projected %q", got)
	}
}
