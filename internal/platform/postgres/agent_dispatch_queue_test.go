package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func agentDispatchTestDatabase(t *testing.T) *Database {
	t.Helper()
	database := workerTestDatabase(t)
	_, err := database.Conn.Exec(`
 CREATE TABLE ai_invocations(id text PRIMARY KEY,user_id text DEFAULT 'owner',space_id text,agent_run_id text,runtime_run_id text DEFAULT '',
  state text,expires_at timestamptz DEFAULT now()+interval '1 day',created_at timestamptz DEFAULT now(),runtime_observed_at timestamptz,
  runtime_heartbeat_at timestamptz,approval_wait_id text DEFAULT '',device_wait_hook_token text DEFAULT '',device_wait_expires_at timestamptz,
  device_wait_context_id text,device_wait_scope_id text DEFAULT '',device_wait_capability text DEFAULT '');
 CREATE TABLE space_runs(id text PRIMARY KEY,owner_user_id text DEFAULT 'owner',space_id text,runtime_run_id text DEFAULT '',state text,
  runtime_phase text DEFAULT '',approval_wait_id text DEFAULT '',device_wait_hook_token text DEFAULT '',device_wait_expires_at timestamptz,
  device_wait_scope_id text DEFAULT '',device_wait_capability text DEFAULT '',execution_owner text DEFAULT 'go',
  runtime_heartbeat_at timestamptz,updated_at timestamptz DEFAULT now());
 CREATE TABLE ai_intervention_waits(id text PRIMARY KEY,user_id text DEFAULT 'owner',run_id text,state text,expires_at timestamptz);
 CREATE TABLE agent_run_tool_approvals(id text PRIMARY KEY,run_id text,invocation_id text,state text,expires_at timestamptz,decided_at timestamptz);
 CREATE TABLE ai_invocation_contexts(id text PRIMARY KEY,invocation_id text,user_id text DEFAULT 'owner',device_id text,opaque_ref text,
  capabilities jsonb DEFAULT '{}',state text,expires_at timestamptz);
 CREATE TABLE agent_run_contexts(id text PRIMARY KEY,run_id text,device_id text,opaque_ref text,capabilities jsonb DEFAULT '{}',state text,expires_at timestamptz);
 CREATE TABLE trusted_devices(id text PRIMARY KEY,user_id text DEFAULT 'owner',last_seen_at timestamptz,revoked_at timestamptz);
 CREATE TABLE agent_runtime_deliveries(id text PRIMARY KEY,run_id text,operation text,state text DEFAULT 'pending',
  available_at timestamptz DEFAULT now(),lease_expires_at timestamptz);
 CREATE TABLE agent_run_jobs(run_id text PRIMARY KEY,task_id text,agent_id text,state text,available_at timestamptz DEFAULT now(),
  lease_expires_at timestamptz,created_at timestamptz DEFAULT now());
 CREATE TABLE space_tasks(id text PRIMARY KEY,assignee_agent_id text,archived_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/20271001070000_agent_dispatch_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err = database.Conn.Exec(up); err != nil {
		t.Fatal(err)
	}
	// Down must apply cleanly and the migration must be reapplicable.
	if _, err = database.Conn.Exec(down); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Conn.Exec(up); err != nil {
		t.Fatal(err)
	}
	return database
}

func dispatchDue(t *testing.T, database *Database, queue string) (time.Duration, bool) {
	t.Helper()
	delay, pending, err := database.NextWorkerDelay(context.Background(), queue)
	if err != nil {
		t.Fatal(queue, err)
	}
	return delay, pending
}

func dispatchExec(t *testing.T, database *Database, statement string) {
	t.Helper()
	if _, err := database.Conn.Exec(statement); err != nil {
		t.Fatal(statement, err)
	}
}

func TestAgentDispatchQueuesIdleAndDeliveryHints(t *testing.T) {
	database := agentDispatchTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime, stopRuntime, err := database.SubscribeWorkerEvents(ctx, "agent-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer stopRuntime()
	tasks, stopTasks, err := database.SubscribeWorkerEvents(ctx, "agent-tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer stopTasks()
	for _, queue := range []string{"agent-runtime", "agent-tasks"} {
		if _, pending := dispatchDue(t, database, queue); pending {
			t.Fatal(queue, "idle queue has a deadline")
		}
	}
	dispatchExec(t, database, `INSERT INTO agent_runtime_deliveries(id,run_id,operation) VALUES('d1','inv','invocation.start')`)
	workerWake(t, runtime)
	workerQuiet(t, tasks)
	if delay, pending := dispatchDue(t, database, "agent-runtime"); !pending || delay != 0 {
		t.Fatal("pending delivery is not due", delay, pending)
	}
	// A failed attempt is scheduled for later, not rescanned immediately.
	dispatchExec(t, database, `UPDATE agent_runtime_deliveries SET available_at=now()+interval '30 seconds'`)
	workerWake(t, runtime)
	if delay, pending := dispatchDue(t, database, "agent-runtime"); !pending || delay < 20*time.Second {
		t.Fatal("retry deadline ignored", delay, pending)
	}
	dispatchExec(t, database, `UPDATE agent_runtime_deliveries SET state='completed'`)
	workerWake(t, runtime)
	if _, pending := dispatchDue(t, database, "agent-runtime"); pending {
		t.Fatal("completed delivery stays due")
	}
	// Writing the same values publishes nothing.
	dispatchExec(t, database, `UPDATE agent_runtime_deliveries SET state='completed'`)
	workerQuiet(t, runtime)
}

func TestAgentDispatchStaleRuntimeFollowsHeartbeatWithoutHints(t *testing.T) {
	database := agentDispatchTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime, stop, err := database.SubscribeWorkerEvents(ctx, "agent-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	dispatchExec(t, database, `INSERT INTO ai_invocations(id,runtime_run_id,state,runtime_heartbeat_at,runtime_observed_at)
  VALUES('inv','rt','running',now()-interval '10 minutes',now()-interval '10 minutes')`)
	workerWake(t, runtime)
	if delay, pending := dispatchDue(t, database, "agent-runtime"); !pending || delay != 0 {
		t.Fatal("stale invocation not due", delay, pending)
	}
	// Heartbeats only move the deadline later; they must not wake workers.
	dispatchExec(t, database, `UPDATE ai_invocations SET runtime_heartbeat_at=now(),runtime_observed_at=now()`)
	workerQuiet(t, runtime)
	if delay, pending := dispatchDue(t, database, "agent-runtime"); !pending || delay < 4*time.Minute || delay > 5*time.Minute {
		t.Fatal("stale deadline does not follow heartbeat", delay, pending)
	}
	// A queued reconcile removes the run from the stale set until it resolves.
	dispatchExec(t, database, `UPDATE ai_invocations SET runtime_heartbeat_at=now()-interval '10 minutes',runtime_observed_at=now()-interval '10 minutes'`)
	dispatchExec(t, database, `INSERT INTO agent_runtime_deliveries(id,run_id,operation,available_at) VALUES('r1','inv','runtime.reconcile',now()+interval '1 minute')`)
	if delay, pending := dispatchDue(t, database, "agent-runtime"); !pending || delay < 50*time.Second {
		t.Fatal("stale invocation with queued reconcile spins", delay, pending)
	}
}

func TestAgentDispatchDeviceWaitsWakeOnlyWhenDeviceReturns(t *testing.T) {
	database := agentDispatchTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime, stopRuntime, err := database.SubscribeWorkerEvents(ctx, "agent-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer stopRuntime()
	tasks, stopTasks, err := database.SubscribeWorkerEvents(ctx, "agent-tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer stopTasks()
	// One transaction: identical hints within it coalesce into one notification.
	dispatchExec(t, database, `INSERT INTO trusted_devices(id,last_seen_at) VALUES('device',now()-interval '10 minutes'),('online',now());
  INSERT INTO space_runs(id,state,device_wait_hook_token,device_wait_expires_at,device_wait_scope_id,device_wait_capability)
  VALUES('run','awaiting_device','hook',now()+interval '1 hour','scope','browser');
  INSERT INTO agent_run_contexts(id,run_id,device_id,opaque_ref,capabilities,state,expires_at)
  VALUES('ctx','run','device','scope','{"browser":true}','attached',now()+interval '1 hour')`)
	workerWake(t, tasks)
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay < 50*time.Minute {
		t.Fatal("offline device wait should sleep until expiry", delay, pending)
	}
	// Heartbeats from an already-online device are silent.
	for i := 0; i < 5; i++ {
		dispatchExec(t, database, `UPDATE trusted_devices SET last_seen_at=now() WHERE id='online'`)
	}
	workerQuiet(t, runtime)
	workerQuiet(t, tasks)
	dispatchExec(t, database, `UPDATE trusted_devices SET last_seen_at=now() WHERE id='device'`)
	workerWake(t, tasks)
	workerWake(t, runtime)
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay != 0 {
		t.Fatal("returned device did not make the wait due", delay, pending)
	}
	dispatchExec(t, database, `UPDATE space_runs SET state='running',runtime_phase='device_resume_pending'`)
	if _, pending := dispatchDue(t, database, "agent-tasks"); pending {
		t.Fatal("resumed device wait stays due")
	}
}

// resumableApprovals applies the scan's own unqueued predicate.
func resumableApprovals(t *testing.T, database *Database) int {
	t.Helper()
	var n int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM agent_run_tool_approvals a JOIN space_runs r ON r.id=a.run_id
  WHERE a.state IN ('approved','denied') AND r.approval_wait_id=a.id AND r.runtime_phase='approval_resume_pending' AND ` + approvalResumeUnqueued).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAgentDispatchApprovalResumeDoesNotSpinOnQueuedDelivery(t *testing.T) {
	database := agentDispatchTestDatabase(t)
	dispatchExec(t, database, `INSERT INTO space_runs(id,state,runtime_phase,approval_wait_id) VALUES('run','running','approval_resume_pending','approval')`)
	dispatchExec(t, database, `INSERT INTO agent_run_tool_approvals(id,run_id,state,expires_at,decided_at) VALUES('approval','run','approved',now()+interval '1 hour',now())`)
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay != 0 {
		t.Fatal("decided approval without delivery is not due", delay, pending)
	}
	if n := resumableApprovals(t, database); n != 1 {
		t.Fatal("scan predicate misses the approval", n)
	}
	// Once its idempotent delivery exists, another scan cannot advance the run.
	dispatchExec(t, database, `INSERT INTO agent_runtime_deliveries(id,run_id,operation,state) VALUES('approval.resume:run:approval','run','approval.resume','failed')`)
	if _, pending := dispatchDue(t, database, "agent-tasks"); pending {
		t.Fatal("approval with existing delivery keeps the queue due")
	}
	if n := resumableApprovals(t, database); n != 0 {
		t.Fatal("scan still selects an approval it cannot advance", n)
	}
}

func TestAgentDispatchJobsRespectAgentExclusivityAndExpiry(t *testing.T) {
	database := agentDispatchTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tasks, stop, err := database.SubscribeWorkerEvents(ctx, "agent-tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	dispatchExec(t, database, `INSERT INTO space_runs(id,state,runtime_run_id,runtime_heartbeat_at) VALUES('busy','running','rt',now()),('next','queued','',NULL)`)
	dispatchExec(t, database, `INSERT INTO agent_run_jobs(run_id,agent_id,state) VALUES('busy','agent','dispatched'),('next','agent','queued')`)
	workerWake(t, tasks)
	// The queued job is blocked by the agent's dispatched job; only the stale
	// recovery deadline of the active run remains.
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay < 11*time.Minute {
		t.Fatal("blocked job is due", delay, pending)
	}
	dispatchExec(t, database, `UPDATE agent_run_jobs SET state='completed' WHERE run_id='busy'`)
	workerWake(t, tasks)
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay != 0 {
		t.Fatal("unblocked job is not due", delay, pending)
	}
	// A terminal run with an open job is cleaned up by the next claim.
	dispatchExec(t, database, `UPDATE agent_run_jobs SET state='leased',lease_expires_at=now()+interval '1 minute' WHERE run_id='next'`)
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay < 50*time.Second {
		t.Fatal("leased job ignores its lease deadline", delay, pending)
	}
	dispatchExec(t, database, `UPDATE space_runs SET state='canceled' WHERE id='next'`)
	if delay, pending := dispatchDue(t, database, "agent-tasks"); !pending || delay != 0 {
		t.Fatal("open job on terminal run is not due", delay, pending)
	}
}
