package api

import (
	"encoding/json"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAIInvocationProgressInterleavesNotesAndTools(t *testing.T) {
	event := func(kind string, payload map[string]string) db.AIInvocationEventRecord {
		raw, _ := json.Marshal(payload)
		return db.AIInvocationEventRecord{EventType: kind, Payload: raw}
	}
	items := aiInvocationProgress([]db.AIInvocationEventRecord{
		event("response.delta", map[string]string{"delta": "Let me check your tabs."}),
		event("tool.started", map[string]string{"toolCallId": "a", "toolName": "tabs.list"}),
		event("tool.completed", map[string]string{"toolCallId": "a", "toolName": "tabs.list"}),
		event("tool.started", map[string]string{"toolCallId": "b", "toolName": "browser.read"}),
		event("tool.failed", map[string]string{"toolCallId": "b", "toolName": "browser.read"}),
		event("response.delta", map[string]string{"delta": "You have three tabs open."}),
		event("assistant.message", map[string]string{"text": "You have three tabs open."}),
	})
	want := []aiProgressItem{
		{Type: "note", Text: "Let me check your tabs."},
		{Type: "tool", ID: "a", Name: "tabs.list", State: "completed"},
		{Type: "tool", ID: "b", Name: "browser.read", State: "failed"},
	}
	if len(items) != len(want) {
		t.Fatalf("%+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Fatalf("item %d: %+v", i, items[i])
		}
	}
}
