package api

import (
	"strings"
	"testing"
)

func TestAgentEntryFollowsWhereTheRequestCameFrom(t *testing.T) {
	cases := map[string]aiInvocationInput{
		"composer":          {Mode: "drawer", Trigger: "message"},
		"cursor":            {Mode: "companion", Trigger: "message"},
		"schedule":          {Mode: "drawer", Trigger: scheduledRunTrigger},
		"email":             {Mode: "drawer", Trigger: inboundTaskTrigger, Prompt: "Task from email. The person forwarded an email"},
		"integration.slack": {Mode: "drawer", Trigger: inboundTaskTrigger, Prompt: "Task from Slack."},
	}
	for want, body := range cases {
		if got := agentEntry(body); got != want {
			t.Errorf("agentEntry(%+v) = %q, want %q", body, got, want)
		}
	}
	if entryGuidance("cursor") != "" {
		t.Error("the cursor's reply rules come from the companion prompt")
	}
}

func TestGlossaryNamesOnlyFamiliesInTheRun(t *testing.T) {
	text := glossaryGuidance([]string{"file.search", "memory.history.search", "web.search"})
	if !strings.Contains(text, "Library") || !strings.Contains(text, "Settings › Memory") {
		t.Fatalf("glossary = %q", text)
	}
	if strings.Contains(text, "Workflows") || strings.Contains(text, "Notebook") {
		t.Fatalf("glossary names families the run doesn't have: %q", text)
	}
	if glossaryGuidance([]string{"web.search"}) != "" {
		t.Fatal("a run without app data needs no glossary")
	}
}
