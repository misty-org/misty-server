package api

import (
	"os"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestScheduleConversationsBelongToTheWorkflowsAgentPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_scheduled_agents_test" {
		t.Skip("requires isolated scheduled agents test database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Schedule owner", "scheduled-owner@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other owner", "scheduled-other@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Research", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conversationID, err := createScheduleConversation(ctx, database, owner.ID, "Research briefing", identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := database.AgentConversationIdentity(ctx, owner.ID, conversationID)
	if err != nil || bound.AgentID != identity.ID {
		t.Fatalf("conversation agent = %+v, error = %v", bound, err)
	}
	if _, err = createScheduleConversation(ctx, database, other.ID, "Foreign schedule", identity.ID); err == nil {
		t.Fatal("another account was allowed to schedule this agent")
	}
}
