package api

import (
	"encoding/json"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
	"testing"
)

func TestAgentSpeechRequiresCompletedReply(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"text": "Saved. [POINT:100,200:Save]"})
	events := []db.AIInvocationEventRecord{{EventType: "assistant.message", Payload: payload}}
	for _, state := range []string{"running", "queued", "failed", "canceled"} {
		if _, err := agentSpeechText(state, events); err == nil {
			t.Fatalf("spoke %s reply", state)
		}
	}
	text, err := agentSpeechText("completed", events)
	if err != nil || text != "Saved." {
		t.Fatalf("text = %q, err = %v", text, err)
	}
	if _, err := agentSpeechText("completed", nil); err == nil {
		t.Fatal("empty speech accepted")
	}
}

func TestAgentSpeechBoundsUnicodeWithoutChangingStoredReply(t *testing.T) {
	original := strings.Repeat("界", 7000)
	payload, _ := json.Marshal(map[string]string{"text": original})
	text, err := agentSpeechText("completed", []db.AIInvocationEventRecord{{EventType: "assistant.message", Payload: payload}})
	if err != nil || len([]rune(text)) > 6000 || !strings.Contains(text, "conversation") {
		t.Fatalf("invalid bounded speech: %v", err)
	}
	var stored map[string]string
	_ = json.Unmarshal(payload, &stored)
	if stored["text"] != original {
		t.Fatal("stored reply changed")
	}
}
