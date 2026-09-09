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
	"github.com/kannachi323/misty/server/internal/platform/security"
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
	document, key := sdkInstallFixture(t, "example.modelrequester")
	document.Scopes = append(document.Scopes, "ai.write")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user, signed, digest); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, user, document.AppID, security.HashToken(uuid.NewString()), "", AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := database.CreateAIInvocationRecord(WithAppExecutionAuthority(ctx, *session), AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
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
	if _, err := database.Conn.Exec(`UPDATE user_app_installations SET granted_scopes='[]' WHERE user_id=$1 AND app_id=$2`, user, document.AppID); err != nil {
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
