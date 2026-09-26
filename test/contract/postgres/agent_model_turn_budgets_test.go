package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentModelTurnBudgetConcurrencyAndRecovery(t *testing.T) {
	for _, kind := range []string{"invocation", "space"} {
		t.Run(kind, func(t *testing.T) {
			database, _, user, _ := sdkInvocationFixture(t)
			ctx := t.Context()
			runtime := uuid.NewString()
			var runID, table string
			if kind == "invocation" {
				run, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
				runID = run.ID
				table = "ai_invocations"
				if _, err := database.ActivateAIInvocationRuntime(ctx, runID, "vercel-workflow", runtime); err != nil {
					t.Fatal(err)
				}
			} else {
				space := createTestSpace(t, database, ctx, user, "Budget test")
				agent, err := database.EnsureAskIdentity(ctx, user, serveragent.InitialSelectedModelID)
				if err != nil {
					t.Fatal(err)
				}
				run, err := database.CreateCreatorAgentRun(ctx, user, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Complete a bounded request"})
				if err != nil {
					t.Fatal(err)
				}
				runID = run.ID
				table = "space_runs"
				if _, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "budget-fixture", 1, time.Minute); err != nil {
					t.Fatal(err)
				}
				if _, err := database.ActivatePersonalAgentTaskRuntime(ctx, runID, "vercel-workflow", runtime); err != nil {
					t.Fatal(err)
				}
			}
			var group sync.WaitGroup
			successes := make(chan string, 32)
			failures := make(chan error, 32)
			for index := range 32 {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					node := fmt.Sprintf("model:%d", index+1)
					err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, node)
					if err == nil {
						successes <- node
					} else {
						failures <- err
					}
				}(index)
			}
			group.Wait()
			close(successes)
			close(failures)
			if len(successes) != 20 || len(failures) != 12 {
				t.Fatalf("budget admitted %d rejected %d", len(successes), len(failures))
			}
			for err := range failures {
				if !errors.Is(err, ErrAgentModelTurnLimit) {
					t.Fatal(err)
				}
			}
			replay := <-successes
			for range 3 {
				if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, replay); err != nil {
					t.Fatalf("lost-response replay: %v", err)
				}
			}
			var count int
			if err := database.Conn.QueryRow(`SELECT count(*) FROM agent_model_turn_claims WHERE run_id=$1`, runID).Scan(&count); err != nil || count != 20 {
				t.Fatalf("replay changed usage: %d %v", count, err)
			}
			if err := database.ReserveAgentModelTurn(ctx, user, runID, "replacement-runtime", replay); !errors.Is(err, ErrSpaceForbidden) {
				t.Fatalf("replacement runtime inherited claim: %v", err)
			}
			if err := database.ReserveAgentModelTurn(ctx, uuid.NewString(), runID, runtime, replay); !errors.Is(err, ErrSpaceForbidden) {
				t.Fatalf("cross-user budget access: %v", err)
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET state='awaiting_approval' WHERE id=$1`, runID); err != nil {
				t.Fatal(err)
			}
			if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, "model:later"); !errors.Is(err, ErrSpaceForbidden) {
				t.Fatalf("model ran during a wait: %v", err)
			}
			if _, err := database.Conn.Exec(`UPDATE `+table+` SET state='running' WHERE id=$1`, runID); err != nil {
				t.Fatal(err)
			}
			if err := database.ReserveAgentModelTurn(ctx, user, runID, runtime, "model:later"); !errors.Is(err, ErrAgentModelTurnLimit) {
				t.Fatalf("wait reset budget: %v", err)
			}
		})
	}
}

func TestAgentModelTurnBudgetRevalidatesAppAuthority(t *testing.T) {
	database, _, user, _ := sdkInvocationFixture(t)
	ctx := t.Context()
	record, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	runtime := uuid.NewString()
	if _, err := database.ActivateAIInvocationRuntime(ctx, record.ID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	if err := database.ReserveAgentModelTurn(ctx, user, record.ID, runtime, "model:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.Exec(`UPDATE ai_invocations SET request_payload=jsonb_set(request_payload,'{_misty_authority}',$2::jsonb) WHERE id=$1`, record.ID, `{"user_id":"`+user+`","app_id":"retired","installation_generation":1,"scopes":["ai.write","capabilities.invoke"]}`); err != nil {
		t.Fatal(err)
	}
	if err := database.ReserveAgentModelTurn(ctx, user, record.ID, runtime, "model:2"); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("revoked app started a model turn: %v", err)
	}
	if _, err := database.AgentRunExecutionBudget(ctx, user, record.ID, runtime, true); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("revoked app resumed execution clock: %v", err)
	}
	if err := database.ReserveAgentModelTurn(ctx, user, record.ID, runtime, "model:"+strings.Repeat("x", 201)); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("unbounded model identity: %v", err)
	}
}

func TestForegroundAgentModelTurnBudget(t *testing.T) {
	database, _, user, _ := sdkInvocationFixture(t)
	ctx := t.Context()
	space := createTestSpace(t, database, ctx, user, "Foreground budget")
	agent, err := database.EnsureAskIdentity(ctx, user, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	lease := AgentExecutionLease{AgentID: agent.ID, SpaceID: space.ID, TaskID: "budget-task", WindowLabel: "main"}
	if err := database.AcquireAgentExecution(ctx, user, lease); err != nil {
		t.Fatal(err)
	}
	var foreground AIInvocationRecord
	for _, scenario := range []struct {
		mode, surface string
		limit         int
	}{
		{"auto", "global", 120}, {"agent", "global", 120}, {"user", "global", 20}, {"team", "global", 20}, {"agent", "sdk", 20}, {"agent", "routine", 20},
	} {
		payload, _ := json.Marshal(map[string]string{"agent_id": agent.ID, "execution_mode": scenario.mode, "task_id": lease.TaskID, "window_label": lease.WindowLabel})
		run, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SpaceID: space.ID, SurfaceID: scenario.surface, Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if run.ModelTurnLimit != scenario.limit {
			t.Fatalf("%+v: limit %d", scenario, run.ModelTurnLimit)
		}
		if scenario.limit == 120 {
			foreground = run
		}
	}
	runtime := uuid.NewString()
	active, err := database.ActivateAIInvocationRuntime(ctx, foreground.ID, "vercel-workflow", runtime)
	if err != nil || active.ModelTurnLimit != 120 {
		t.Fatalf("activate: %+v %v", active, err)
	}
	validated, err := database.ValidateAIInvocationRuntime(ctx, foreground.ID, runtime)
	if err != nil || validated.ModelTurnLimit != 120 {
		t.Fatalf("context: %+v %v", validated, err)
	}
	for i := 1; i <= 120; i++ {
		if err := database.ReserveAgentModelTurn(ctx, user, foreground.ID, runtime, fmt.Sprintf("model:%d", i)); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if err := database.ReserveAgentModelTurn(ctx, user, foreground.ID, runtime, "model:121"); !errors.Is(err, ErrAgentModelTurnLimit) {
		t.Fatalf("exceeded limit: %v", err)
	}
	if err := database.ReserveAgentModelTurn(ctx, user, foreground.ID, runtime, "model:120"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	// Admission retries must not replace the pinned larger budget or its claims.
	retry := foreground
	retry.RequestPayload = json.RawMessage(`{}`)
	stored, created, err := database.CreateAIInvocationRecord(ctx, retry)
	if err != nil || created || stored.ModelTurnLimit != 120 {
		t.Fatalf("retry changed budget: %+v %v %v", stored, created, err)
	}
	if err := database.ReleaseAgentExecution(ctx, user, lease.TaskID); err != nil {
		t.Fatal(err)
	}
	foreground.ID = "invocation_" + uuid.NewString()
	foreground.IdempotencyKey = uuid.NewString()
	if _, _, err := database.CreateAIInvocationRecord(ctx, foreground); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("missing lease admitted: %v", err)
	}
}
