package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeviceToolActivityShowsTheQueryAndLinksFound(t *testing.T) {
	output := json.RawMessage(`{"tool":"web.search","duration_ms":40,"success":true,
		"device_arguments":{"query":"tea  prices"},"device_result":{"results":[{"title":"Tea","url":"https://tea.example/p"}]}}`)
	activity := deviceToolActivity("completed", output)
	if activity == nil || activity.Detail != "tea prices" || activity.Result != "1 result" || len(activity.Rows) != 1 || activity.Rows[0].Title != "Tea" {
		t.Fatalf("activity = %+v", activity)
	}
	if deviceToolActivity("failed", output) != nil || deviceToolActivity("completed", json.RawMessage(`{"tool":"web.search"}`)) != nil {
		t.Fatal("only a finished device call carries activity")
	}
}

func TestOnlyKnownDeviceToolsJoinARunsPrompt(t *testing.T) {
	if got := (agentRuntimeContextRequest{DeviceTools: []string{"web.search", "file.delete"}}).deviceTools(); len(got) != 1 || got[0] != webSearchTool {
		t.Fatalf("device tools = %v", got)
	}
	if got := (agentRuntimeContextRequest{}).deviceTools(); got != nil {
		t.Fatalf("no device tools = %v", got)
	}
	guidance := agentCapabilityGuidance([]string{webSearchTool}, false)
	if !strings.Contains(guidance, "call web_search") || strings.Contains(agentCapabilityGuidance([]string{"web.fetch"}, false), "web_search") {
		t.Fatal("the prompt describes web_search only when the device has it")
	}
}
