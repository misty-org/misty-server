package api

import (
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMatchWorkflowsRanksBySharedWords(t *testing.T) {
	methods := []db.AgentMethod{
		{ID: "digest", Definition: db.AgentMethodDefinition{Title: "Hacker News AI digest", Instructions: "Top stories about AI agents with links"}},
		{ID: "invoices", Definition: db.AgentMethodDefinition{Title: "Pay invoices", Instructions: "Find unpaid invoices in Gmail"}},
		{ID: "stories", Definition: db.AgentMethodDefinition{Title: "Story ideas", Instructions: "Brainstorm"}},
	}
	matches := matchWorkflows(methods, "top hacker news stories about AI agents", 3)
	if len(matches) != 1 || matches[0]["id"] != "digest" {
		t.Fatalf("matches = %+v", matches)
	}
	if got := matchWorkflows(methods, "the weather", 3); len(got) != 0 {
		t.Fatalf("unrelated request matched %+v", got)
	}
}
