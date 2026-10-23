package api

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

func TestPromptFromRunUsesPinnedInvocationInput(t *testing.T) {
	run := &db.SpaceRun{Input: json.RawMessage(`{"prompt":"approved operation","untrusted":"ignored"}`)}
	if prompt := TestingPromptFromRun(run); prompt != "approved operation" {
		t.Fatalf("promptFromRun = %q", prompt)
	}
}

func TestCanonicalRunResponsePreservesConversationOutcome(t *testing.T) {
	tests := []struct {
		name      string
		run       db.SpaceRun
		eventType string
		text      string
	}{
		{name: "completed output", run: db.SpaceRun{ID: "run_done", State: "completed", Outputs: json.RawMessage(`{"text":"Finished safely"}`)}, eventType: "agent_message", text: "Finished safely"},
		{name: "failed output", run: db.SpaceRun{ID: "run_failed", State: "failed", ErrorMessage: "Integration expired"}, eventType: "error", text: "Integration expired"},
		{name: "canceled output", run: db.SpaceRun{ID: "run_canceled", State: "canceled"}, eventType: "agent_message", text: "canceled"},
		{name: "queued device run", run: db.SpaceRun{ID: "run_device", State: "running", Outputs: json.RawMessage(`{"job_id":"job_one"}`)}, eventType: "agent_message", text: "run_device"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eventType, text := TestingCanonicalRunResponse(&test.run)
			if eventType != test.eventType || !strings.Contains(text, test.text) {
				t.Fatalf("canonicalRunResponse() = %q, %q", eventType, text)
			}
		})
	}
}
