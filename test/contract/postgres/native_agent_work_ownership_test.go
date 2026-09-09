package db

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestGoAgentSchedulerLeavesNativeOwnedJobsUntouched(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Ownership", "native-work-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Ownership")
	if err != nil {
		t.Fatal(err)
	}
	queue := func(name string) *SpaceRun {
		agent, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
		if err != nil {
			t.Fatal(err)
		}
		run, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: name})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	native, legacy := queue("Native"), queue("Go")
	if _, err := database.Conn.ExecContext(ctx, `UPDATE space_runs SET execution_owner='hono',state='completed' WHERE id=$1`, native.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "go-owner-test", 2, time.Minute)
	if err != nil || len(jobs) != 1 || jobs[0].Run.ID != legacy.ID {
		t.Fatalf("Go claims: %#v err=%v", jobs, err)
	}
	var state string
	if err := database.Conn.QueryRowContext(ctx, `SELECT state FROM agent_run_jobs WHERE run_id=$1`, native.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "queued" {
		t.Fatalf("Go modified native terminal job: %s", state)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE space_runs SET state='running',runtime_heartbeat_at=now()-interval '1 day' WHERE id=$1`, native.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE agent_run_jobs SET state='dispatched' WHERE run_id=$1`, native.ID); err != nil {
		t.Fatal(err)
	}
	count, err := database.ReconcileStalePersonalAgentTaskRuns(ctx, time.Now().Add(-time.Hour), 20)
	if err != nil || count != 0 {
		t.Fatalf("Go reconciled native job: count=%d err=%v", count, err)
	}
}
