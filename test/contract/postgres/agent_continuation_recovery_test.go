package db

import (
	"encoding/json"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
	"time"
)

func TestRecoveryPreservesRuntimeAndExactDeviceWait(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Recovery", "recovery@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Recovery")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Resume safely", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "recovery-worker", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	run, err = database.MarkPersonalAgentTaskRunDispatched(ctx, run.ID, "recovery-worker", "vercel-workflow", "pinned-runtime")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE space_runs SET runtime_heartbeat_at=NOW()-INTERVAL '1 day' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	count, err := database.ReconcileStalePersonalAgentTaskRuns(ctx, time.Now().Add(-time.Hour), 20)
	if err != nil || count != 1 {
		t.Fatalf("reconcile %d %v", count, err)
	}
	jobs, err = database.ClaimPersonalAgentTaskRunJobs(ctx, "another-worker", 1, time.Minute)
	if err != nil || len(jobs) != 0 {
		t.Fatal("recovery restarted the original prompt")
	}
	var runtimeID, state string
	if err := database.Conn.QueryRowContext(ctx, `SELECT runtime_run_id,state FROM space_runs WHERE id=$1`, run.ID).Scan(&runtimeID, &state); err != nil || runtimeID != "pinned-runtime" || state != "running" {
		t.Fatalf("lost binding: %s %s %v", runtimeID, state, err)
	}
	if err := database.AwaitAgentRunDeviceTarget(ctx, run.ID, runtimeID, "wait-one", "mail-account-one", "browser.click"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueueAgentDeviceResume(ctx, AgentDeviceWait{RunID: run.ID, HookToken: "wait-one", Available: true}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var device AgentRuntimeDelivery
	var payload AgentContinuation
	for _, d := range deliveries {
		if d.Operation == "device.resume" {
			device = d
			if err := json.Unmarshal(d.Payload, &payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if device.ID == "" {
		t.Fatal("device continuation lost")
	}
	// Runtime accepted the first hook and entered another wait before the worker
	// acknowledged delivery. The first acknowledgement must preserve the second.
	if err := database.AwaitAgentRunDeviceTarget(ctx, run.ID, runtimeID, "wait-two", "mail-account-one", "browser.click"); err != nil {
		t.Fatal(err)
	}
	if err := database.FinishAgentContinuation(ctx, device, payload); err != nil {
		t.Fatal(err)
	}
	var hook string
	if err := database.Conn.QueryRowContext(ctx, `SELECT device_wait_hook_token FROM space_runs WHERE id=$1`, run.ID).Scan(&hook); err != nil || hook != "wait-two" {
		t.Fatalf("new wait overwritten: %s %v", hook, err)
	}
	if _, err := database.CancelPersonalAgentTaskRunForOwner(ctx, owner.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	current, err := database.AgentContinuationCurrent(ctx, device, payload)
	if err != nil || current {
		t.Fatal("cancelled run resumed")
	}
	deliveries, err = database.ClaimAgentRuntimeDeliveries(ctx, 20)
	if err != nil || len(deliveries) != 1 || deliveries[0].Operation != "runtime.cancel" {
		t.Fatalf("cancellation intent lost: %#v %v", deliveries, err)
	}
}
