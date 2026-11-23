package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCleanAgentStatusesKeepsOnlyFixedCodesAndClaimPaths(t *testing.T) {
	clean := cleanAgentStatuses([]agentStatus{
		{ID: "a1", Name: " Researcher ", State: "working", Doing: "browser", Files: []string{"notes.md", "src/auth.rs", "line one\nline two", "/Users/me/key.pem", "../up.txt", "src//x.rs", "C:\\x.rs"}},
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
	if len(got.Files) != 2 || got.Files[0] != "notes.md" || got.Files[1] != "src/auth.rs" {
		t.Fatalf("files = %v, want only relative paths inside the folder", got.Files)
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

func TestBroadcastAgentsReachesOnlyTheSpacesOtherMembers(t *testing.T) {
	f := newSpaceRouteFixture(t)
	s := NewRealtimeService(f.database, "")
	sender := testRealtimeClient(s, f.owner, 4)
	senderPhone := testRealtimeClient(s, f.owner, 4)
	member := testRealtimeClient(s, f.member, 4)
	memberLaptop := testRealtimeClient(s, f.member, 4)
	other := testRealtimeClient(s, f.other, 4)
	stranger := testRealtimeClient(s, f.stranger, 4)

	s.broadcastAgents(sender, f.space.ID, []agentStatus{
		{ID: "a1", Name: "Builder", State: "working", Doing: "shell", Files: []string{"src/app.ts"}},
		{ID: "a2", Name: "Writer", State: "working", Doing: "a prompt the model wrote"},
	})

	for _, client := range []*RealtimeClient{member, memberLaptop, other} {
		select {
		case raw := <-nextAgents(client):
			var got struct {
				Type    string        `json:"type"`
				SpaceID string        `json:"space_id"`
				UserID  string        `json:"user_id"`
				Agents  []agentStatus `json:"agents"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got.Type != "agents" || got.SpaceID != f.space.ID || got.UserID != f.owner {
				t.Fatalf("envelope = %+v", got)
			}
			if len(got.Agents) != 1 || got.Agents[0].Name != "Builder" || got.Agents[0].Files[0] != "src/app.ts" {
				t.Fatalf("free text or a bad status got through: %+v", got.Agents)
			}
		default:
			t.Fatal("a member's device heard nothing")
		}
	}
	if agentsHeard(sender)+agentsHeard(senderPhone)+agentsHeard(stranger) != 0 {
		t.Fatal("the sender or someone outside the Space heard the status")
	}

	lines, err := f.database.ListAgentActivity(t.Context(), f.member, f.space.ID, 10)
	if err != nil || len(lines) != 1 {
		t.Fatalf("activity = %+v, %v", lines, err)
	}

	stranger.lastAgentsAt = time.Time{}
	s.broadcastAgents(stranger, f.space.ID, []agentStatus{{ID: "x", Name: "Spy", State: "working", Doing: "web"}})
	if agentsHeard(member)+agentsHeard(memberLaptop)+agentsHeard(other)+agentsHeard(sender) != 0 {
		t.Fatal("a stranger's status was delivered")
	}
}

func isAgents(raw []byte) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &envelope) == nil && envelope.Type == "agents"
}

func nextAgents(client *RealtimeClient) <-chan []byte {
	out := make(chan []byte, 1)
	for {
		select {
		case raw := <-client.Send:
			if isAgents(raw) {
				out <- raw
				return out
			}
		default:
			return out
		}
	}
}

func agentsHeard(client *RealtimeClient) int {
	count := 0
	for {
		select {
		case raw := <-client.Send:
			if isAgents(raw) {
				count++
			}
		default:
			return count
		}
	}
}
