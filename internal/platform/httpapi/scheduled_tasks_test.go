package api

import (
	"os"
	"strings"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestScheduledTaskAgentAssignmentPostgres(t *testing.T) {
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
	service := &AIService{database: database}
	conversationID, err := service.createScheduledTaskConversation(ctx, owner.ID, "Research briefing", identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := database.AgentConversationIdentity(ctx, owner.ID, conversationID)
	if err != nil || bound.AgentID != identity.ID {
		t.Fatalf("conversation agent = %+v, error = %v", bound, err)
	}
	if _, err = service.createScheduledTaskConversation(ctx, other.ID, "Foreign task", identity.ID); err == nil {
		t.Fatal("another account was allowed to schedule this agent")
	}
	now := time.Now().UTC()
	task, err := database.CreateScheduledTask(ctx, owner.ID, conversationID, db.ScheduledTask{Title: "Research briefing", Prompt: "Find useful papers", Enabled: true, ScheduledTaskSchedule: db.ScheduledTaskSchedule{Cadence: "daily", LocalTime: "09:00", Weekday: 1, MonthDay: 1, Timezone: "UTC"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if task.AgentID != identity.ID {
		t.Fatalf("stored agent = %q", task.AgentID)
	}
	// Existing schedules acquire their agent from their conversation during migration.
	tx, err := database.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `ALTER TABLE scheduled_tasks DROP COLUMN agent_id`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../postgres/migrations/20270928010000_scheduled_task_agents.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var backfilled string
	if err = tx.QueryRowContext(ctx, `SELECT agent_id FROM scheduled_tasks WHERE id=$1`, task.ID).Scan(&backfilled); err != nil || backfilled != identity.ID {
		t.Fatalf("legacy assignment = %q, %v", backfilled, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	task.Prompt = "Find useful papers about agent interfaces"
	task.AgentID = "attempted-reassignment"
	updated, err := database.UpdateScheduledTask(ctx, owner.ID, *task, now)
	if err != nil || updated.AgentID != identity.ID {
		t.Fatalf("update changed assignment: %+v, %v", updated, err)
	}
	if _, err = database.Conn.ExecContext(ctx, `DELETE FROM misty_ask_conversations WHERE id=$1`, conversationID); err != nil {
		t.Fatal(err)
	}
	tasks, err := database.ScheduledTasks(ctx, owner.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after conversation removal: %+v, %v", tasks, err)
	}
	if tasks[0].AgentID != identity.ID || tasks[0].ConversationID != "" {
		t.Fatalf("lost task owner: %+v", tasks[0])
	}
	restoredID, err := service.createScheduledTaskConversation(ctx, owner.ID, tasks[0].Title, tasks[0].AgentID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := database.AgentConversationIdentity(ctx, owner.ID, restoredID)
	if err != nil || restored.AgentID != identity.ID {
		t.Fatalf("restored with wrong agent: %+v, %v", restored, err)
	}
}
