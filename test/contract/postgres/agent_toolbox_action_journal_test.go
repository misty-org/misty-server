package db

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentEffectClaimIsExclusiveAcrossWorkers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Concurrent", "concurrent-effects@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	action := AgentToolboxAction{IdempotencyKey: "same-logical-send", UserID: user.ID, ToolName: "messages.send", Risk: "write", AuditEvent: "message.sent", Source: "test", Request: json.RawMessage(`{"text":"hello"}`)}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) {
			close(entered)
			<-release
			return json.RawMessage(`{"message_id":"one"}`), nil
		})
		finished <- err
	}()
	<-entered
	var workers sync.WaitGroup
	for range 10 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) { t.Error("second worker executed claimed effect"); return nil, nil })
			if !errors.Is(err, ErrAgentToolboxActionInProgress) {
				t.Errorf("exclusive claim: %v", err)
			}
		}()
	}
	workers.Wait()
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	result, err := database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) { t.Error("confirmed send repeated"); return nil, nil })
	if err != nil || len(result) == 0 {
		t.Fatalf("replay %s %v", result, err)
	}
}

func TestAgentToolboxActionJournalReplaysSuccessAndRetriesOnlyReads(t *testing.T) {
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
	action.Risk = "read"
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

func TestAgentToolboxActionJournalNeverRetriesUncertainWrite(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Uncertain", "uncertain@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	action := AgentToolboxAction{IdempotencyKey: "uncertain-send", UserID: user.ID, ToolName: "messages.send", AuditEvent: "message.sent", Risk: "write", Source: "contract", Request: json.RawMessage(`{"body":"hello"}`)}
	calls := 0
	execute := func() (json.RawMessage, error) { calls++; return nil, errors.New("response lost after send") }
	for range 2 {
		if _, err := database.JournalAgentToolboxAction(ctx, action, execute); !errors.Is(err, ErrAgentToolboxActionUnknown) {
			t.Fatalf("uncertain write: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
	action.Request = json.RawMessage(`{"body":"changed"}`)
	if _, err := database.JournalAgentToolboxAction(ctx, action, execute); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed effect accepted: %v", err)
	}
}

func TestAgentToolboxActionJournalResumesUndispatchedDeviceAction(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Device", "device-journal@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	action := AgentToolboxAction{IdempotencyKey: "device-action", UserID: user.ID, ToolName: "browser.click", AuditEvent: "browser.click", Risk: "write", Source: "contract", Request: json.RawMessage(`{}`)}
	if _, err := database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) { return nil, ErrAgentToolboxNotAttempted }); !errors.Is(err, ErrAgentToolboxNotAttempted) {
		t.Fatal(err)
	}
	result, err := database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) { return json.RawMessage(`{"attempted":true}`), nil })
	if err != nil || len(result) == 0 {
		t.Fatalf("did not resume: %v", err)
	}
}
