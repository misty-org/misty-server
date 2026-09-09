package db

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func executionClockFixture(t *testing.T, kind string) (*Database, string, string, string, string) {
	t.Helper()
	database, _, user, _ := sdkInvocationFixture(t)
	ctx := t.Context()
	runtime := uuid.NewString()
	if kind == "invocation" {
		run, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = database.ActivateAIInvocationRuntime(ctx, run.ID, "vercel-workflow", runtime); err != nil {
			t.Fatal(err)
		}
		return database, user, run.ID, runtime, "ai_invocations"
	}
	space := createTestSpace(t, database, ctx, user, "Execution budget")
	agent, err := database.EnsureAskIdentity(ctx, user, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, user, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Do bounded work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ClaimPersonalAgentTaskRunJobs(ctx, "clock-fixture", 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ActivatePersonalAgentTaskRuntime(ctx, run.ID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	return database, user, run.ID, runtime, "space_runs"
}

func TestAgentExecutionClockWaitsRecoveryAndExhaustion(t *testing.T) {
	for _, kind := range []string{"space", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			database, user, run, runtime, table := executionClockFixture(t, kind)
			ctx := t.Context()
			read := func(begin bool) *AgentExecutionBudget {
				t.Helper()
				value, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, begin)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			initial := read(false)
			if initial.Version != 1 || initial.LimitMS != 1800000 || initial.ConsumedMS != 0 || initial.Active {
				t.Fatalf("dispatch consumed time: %#v", initial)
			}
			first := read(true)
			if !first.Active || first.Deadline == nil {
				t.Fatal("admission did not start clock")
			}
			var wg sync.WaitGroup
			for range 24 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					next, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, true)
					if err != nil || next.Deadline == nil || !next.Deadline.Equal(*first.Deadline) {
						t.Errorf("competing/retried admission reset deadline: %#v %v", next, err)
					}
				}()
			}
			wg.Wait()
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET execution_active_at=clock_timestamp()-INTERVAL '5 minutes' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET state='awaiting_approval' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			paused := read(false)
			if paused.Active || paused.Deadline != nil || paused.ConsumedMS < 300000 || paused.ConsumedMS > 301000 {
				t.Fatalf("wait failed to debit and pause: %#v", paused)
			}
			if _, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, true); !errors.Is(err, ErrSpaceForbidden) {
				t.Fatalf("execution admitted in wait: %v", err)
			}
			// A long wait/recovery record must not be reconstructed from wall-clock age.
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET updated_at=NOW()-INTERVAL '19 hours',runtime_heartbeat_at=NOW()-INTERVAL '19 hours' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if got := read(false); got.ConsumedMS != paused.ConsumedMS {
				t.Fatalf("waiting consumed time: %#v", got)
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET state='running' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if got := read(false); got.Active || got.ConsumedMS != paused.ConsumedMS {
				t.Fatalf("undelivered resume started clock: %#v", got)
			}
			resumed := read(true)
			if !resumed.Active || resumed.ConsumedMS != paused.ConsumedMS {
				t.Fatalf("resume reset allowance: %#v", resumed)
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET execution_active_at=clock_timestamp()-INTERVAL '2 minutes' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			nextState := "awaiting_approval"
			if kind == "space" {
				nextState = "awaiting_device"
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET state=$2 WHERE id=$1`, run, nextState); err != nil {
				t.Fatal(err)
			}
			if got := read(false); got.Active || got.ConsumedMS < 420000 || got.ConsumedMS > 422000 {
				t.Fatalf("second wait lost consumed time: %#v", got)
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET state='running',execution_consumed_ms=execution_limit_ms WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := database.AgentRunExecutionBudget(ctx, user, run, runtime, true); !errors.Is(err, ErrAgentExecutionTimeLimit) {
				t.Fatalf("exhausted run resumed: %v", err)
			}
			if err := database.ReserveAgentModelTurn(ctx, user, run, runtime, "model:1"); !errors.Is(err, ErrAgentExecutionTimeLimit) {
				t.Fatalf("exhausted run started model: %v", err)
			}
			var claims int
			if err := database.Conn.QueryRow(`SELECT count(*) FROM agent_model_turn_claims WHERE run_id=$1`, run).Scan(&claims); err != nil || claims != 0 {
				t.Fatalf("failed time admission consumed a model claim: %d %v", claims, err)
			}
			if _, err := database.AgentRunExecutionBudget(ctx, uuid.NewString(), run, runtime, false); !errors.Is(err, ErrSpaceForbidden) {
				t.Fatalf("cross-user clock: %v", err)
			}
			if _, err := database.AgentRunExecutionBudget(ctx, user, run, "replacement-runtime", false); !errors.Is(err, ErrSpaceForbidden) {
				t.Fatalf("replacement runtime: %v", err)
			}
		})
	}
}

func TestAgentExecutionClockTransitionsAreAtomicAndLegacyIsPinned(t *testing.T) {
	for _, kind := range []string{"space", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			database, user, run, runtime, table := executionClockFixture(t, kind)
			first, err := database.AgentRunExecutionBudget(t.Context(), user, run, runtime, true)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := database.Conn.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(`UPDATE `+table+` SET state='awaiting_approval' WHERE id=$1`, run); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			recovered, err := database.AgentRunExecutionBudget(t.Context(), user, run, runtime, false)
			if err != nil || !recovered.Active || !recovered.Deadline.Equal(*first.Deadline) {
				t.Fatalf("rolled-back wait changed clock: %#v %v", recovered, err)
			}
			if _, err = database.Conn.Exec(`UPDATE `+table+` SET state='failed' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			stopped, err := database.AgentRunExecutionBudget(t.Context(), user, run, runtime, false)
			if err != nil || stopped.Active {
				t.Fatalf("terminal run kept ticking: %#v %v", stopped, err)
			}
			// Simulate an admission that predated the additive migration.
			if _, err = database.Conn.Exec(`UPDATE `+table+` SET state='running',execution_budget_version=0 WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			legacy, err := database.AgentRunExecutionBudget(t.Context(), user, run, runtime, true)
			if err != nil || legacy.Version != 0 || legacy.Active || legacy.Deadline != nil {
				t.Fatalf("old admission silently upgraded: %#v %v", legacy, err)
			}
		})
	}
}
