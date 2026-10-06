package agent

import "testing"

func TestVoiceConversationStartTaskTakesOnlyTheRequest(t *testing.T) {
	config := VoiceConversationConfig("[]")["config"].(map[string]any)
	foundStart := false
	for _, tool := range config["tools"].([]map[string]any) {
		parameters := tool["parameters"].(map[string]any)
		properties := parameters["properties"].(map[string]any)
		for _, field := range []string{"needs_browser", "needs_screen"} {
			if _, exists := properties[field]; exists {
				t.Fatalf("routing flag %s exposed on %s", field, tool["name"])
			}
		}
		if tool["name"] != "start_task" {
			continue
		}
		foundStart = true
		if _, ok := properties["instruction"]; !ok || parameters["additionalProperties"] != false {
			t.Fatal("task arguments must be the closed instruction")
		}
	}
	if !foundStart {
		t.Fatal("missing start_task")
	}
}
