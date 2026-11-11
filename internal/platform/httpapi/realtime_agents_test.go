package api

import "testing"

func TestCleanAgentStatusesKeepsOnlyFixedCodesAndFileNames(t *testing.T) {
	clean := cleanAgentStatuses([]agentStatus{
		{ID: "a1", Name: " Researcher ", State: "working", Doing: "browser", Files: []string{"notes.md", "src/secret.rs", "line one\nline two"}},
		// A model's words can't ride in as a state or a kind of work.
		{ID: "a2", Name: "Writer", State: "working", Doing: "Summarising the quarterly report"},
		{ID: "a3", Name: "Writer", State: "thinking hard", Doing: ""},
		{ID: "", Name: "Nameless", State: "resting", Doing: ""},
	})
	if len(clean) != 1 {
		t.Fatalf("kept %d statuses, want 1: %+v", len(clean), clean)
	}
	got := clean[0]
	if got.Name != "Researcher" || got.State != "working" || got.Doing != "browser" {
		t.Fatalf("status = %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0] != "notes.md" {
		t.Fatalf("files = %v, want only the bare file name", got.Files)
	}
}

func TestCleanAgentStatusesCapsHowManyAreSent(t *testing.T) {
	many := make([]agentStatus, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, agentStatus{ID: string(rune('a' + i)), Name: "Agent", State: "resting"})
	}
	if got := len(cleanAgentStatuses(many)); got != maxAgentStatuses {
		t.Fatalf("kept %d statuses, want %d", got, maxAgentStatuses)
	}
}
