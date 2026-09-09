package db

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
	"time"
)

func timerRoutineFixture(t *testing.T) (*Database, string, string, string) {
	t.Helper()
	database, _, user, _ := sdkInvocationFixture(t)
	ctx := t.Context()
	id := uuid.NewString()
	raw := json.RawMessage(`{"protocol":1,"name":"Timed routine","trigger":{"kind":"manual"},"budget":{},"steps":[{"id":"first","label":"First","kind":"wait","until":{"kind":"literal","value":"2026-09-08T09:00:00Z"}},{"id":"second","label":"Second","kind":"wait","until":{"kind":"literal","value":"2026-09-08T10:00:00Z"}}]}`)
	if _, err := database.SaveRoutineDraft(ctx, user, id, 0, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AdmitManualRoutine(ctx, user, id, uuid.NewString(), 1, []byte(`{}`)); err == nil {
		t.Fatal("wait admitted with its adapter disabled")
	}
	run, err := database.AdmitManualRoutine(ctx, user, id, uuid.NewString(), 1, []byte(`{}`), RoutineAdmissionOptions{TimedWaits: true})
	if err != nil {
		t.Fatal(err)
	}
	runtime := uuid.NewString()
	if _, err := database.ActivateAIInvocationRuntime(ctx, run.Execution.RunID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	return database, user, run.Execution.RunID, runtime
}
func TestRoutineTimerPausesClockAndRejectsStaleWake(t *testing.T) {
	database, user, run, runtime := timerRoutineFixture(t)
	ctx := t.Context()
	first, err := database.OpenRoutineWait(ctx, user, run, runtime, "first", time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond))
	if err != nil || first.State != "completed" {
		t.Fatalf("past wait: %#v %v", first, err)
	}
	if _, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, true); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.Exec(`UPDATE ai_invocations SET execution_active_at=clock_timestamp()-INTERVAL '10 seconds' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	second, err := database.OpenRoutineWait(ctx, user, run, runtime, "second", until)
	if err != nil || second.State != "waiting" {
		t.Fatalf("open timer: %#v %v", second, err)
	}
	budget, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, false)
	if err != nil || budget.Active || budget.ConsumedMS < 10000 {
		t.Fatalf("clock not paused: %#v %v", budget, err)
	}
	if _, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, true); err == nil {
		t.Fatal("execution resumed during timer")
	}
	if _, err := database.OpenRoutineWait(ctx, user, run, runtime, "second", until.Add(time.Minute)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("deadline changed on replay: %v", err)
	}
	early, err := database.ResumeRoutineWait(ctx, user, run, runtime, "second", second.WaitID)
	if err != nil || early.State != "waiting" {
		t.Fatalf("early wake: %#v %v", early, err)
	}
	if _, err := database.ResumeRoutineWait(ctx, user, run, runtime, "second", first.WaitID); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("wrong wait identity: %v", err)
	}
	if _, err := database.ResumeRoutineWait(ctx, user, run, "replacement", "second", second.WaitID); err == nil {
		t.Fatal("replacement runtime resumed")
	}
	if _, err := database.ResumeRoutineWait(ctx, user, run, runtime, "first", first.WaitID); err != nil {
		t.Fatal(err)
	}
	status, err := database.RoutineRun(ctx, user, run)
	if err != nil || status.State != "awaiting_timer" || status.Wait == nil || status.Wait.WaitID != second.WaitID {
		t.Fatalf("stale wake cleared later timer: %#v %v", status, err)
	}
	after, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, false)
	if err != nil || after.Active || after.ConsumedMS != budget.ConsumedMS {
		t.Fatalf("waiting consumed execution time: %#v %v", after, err)
	}
	if err := database.RequestRoutineCancellation(ctx, user, run); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResumeRoutineWait(ctx, user, run, runtime, "second", second.WaitID); err == nil {
		t.Fatal("cancelled timer resumed")
	}
}
func TestRoutineTimerExpiryPersistsInterruptionDelivery(t *testing.T) {
	database, user, run, runtime := timerRoutineFixture(t)
	ctx := t.Context()
	// Seed a wait admitted more than a day ago before its immutable deadline is set.
	if _, err := database.Conn.Exec(`UPDATE misty_routine_waits SET state='waiting',until_at=NOW()-INTERVAL '2 hours',expires_at=NOW()-INTERVAL '1 hour' WHERE invocation_id=$1 AND step_id='first'`, run); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.Exec(`UPDATE ai_invocations SET state='awaiting_timer' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if err := database.ExpireRoutineWaits(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.ExpireRoutineWaits(ctx); err != nil {
		t.Fatal(err)
	}
	waits, err := database.RoutineWaits(ctx, user, run)
	if err != nil || waits[0].State != "expired" {
		t.Fatalf("expiry: %#v %v", waits, err)
	}
	var deliveries int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM agent_runtime_deliveries WHERE run_id=$1 AND operation='runtime.cancel'`, run).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("expiry delivery: %d %v", deliveries, err)
	}
	if _, err := database.ResumeRoutineWait(ctx, user, run, runtime, "first", waits[0].WaitID); err == nil {
		t.Fatal("expired wait resumed execution")
	}
}
