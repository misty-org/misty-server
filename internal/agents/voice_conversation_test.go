package agent

import (
	"strings"
	"testing"
)

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

func TestVoiceConversationRoutesScreenQuestionsToShowOnScreen(t *testing.T) {
	config := VoiceConversationConfig("[]")["config"].(map[string]any)
	found := false
	for _, tool := range config["tools"].([]map[string]any) {
		if tool["name"] != "show_on_screen" {
			continue
		}
		found = true
		parameters := tool["parameters"].(map[string]any)
		if _, ok := parameters["properties"].(map[string]any)["instruction"]; !ok || parameters["additionalProperties"] != false {
			t.Fatal("show_on_screen takes only the closed question")
		}
	}
	if !found || !strings.Contains(config["instructions"].(string), "call show_on_screen") {
		t.Fatal("screen questions must route to show_on_screen")
	}
}
