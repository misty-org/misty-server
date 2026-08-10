package db

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentToolboxActionJournalReplaysSuccessAndRetriesFailure(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Toolbox Journal", "toolbox-journal@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}

	action := AgentToolboxAction{
		IdempotencyKey: "contract-toolbox-success", UserID: user.ID, SessionID: "session-1",
		ToolName: "tasks.update", AuditEvent: "task.updated", Risk: "write", Source: "contract",
		Request: json.RawMessage(`{"task_id":"task-1"}`),
	}
	executions := 0
	execute := func() (json.RawMessage, error) {
		executions++
		return json.RawMessage(`{"ok":true}`), nil
	}
	first, err := database.JournalAgentToolboxAction(ctx, action, execute)
	if err != nil || string(first) != `{"ok": true}` && string(first) != `{"ok":true}` {
		t.Fatalf("first result=%s err=%v", first, err)
	}
	replayed, err := database.JournalAgentToolboxAction(ctx, action, execute)
	var firstValue, replayedValue any
	firstDecodeErr := json.Unmarshal(first, &firstValue)
	replayDecodeErr := json.Unmarshal(replayed, &replayedValue)
	if err != nil || firstDecodeErr != nil || replayDecodeErr != nil || !reflect.DeepEqual(replayedValue, firstValue) || executions != 1 {
		t.Fatalf("replayed result=%s executions=%d err=%v", replayed, executions, err)
	}

	action.IdempotencyKey = "contract-toolbox-retry"
	attempts := 0
	failed := errors.New("temporary tool failure")
	result, err := database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) {
		attempts++
		if attempts == 1 {
			return nil, failed
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	if !errors.Is(err, failed) {
		t.Fatalf("failed result=%s err=%v", result, err)
	}
	result, err = database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) {
		attempts++
		return json.RawMessage(`{"ok":true}`), nil
	})
	if err != nil || attempts != 2 || len(result) == 0 {
		t.Fatalf("retry result=%s attempts=%d err=%v", result, attempts, err)
	}
}
