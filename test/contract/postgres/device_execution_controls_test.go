package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
	"testing"
	"time"
)

func TestDeviceExecutionControls(t *testing.T) {
	database, _, user, _ := sdkInvocationFixture(t)
	ctx := t.Context()
	space := createTestSpace(t, database, ctx, user, "Device controls")
	invocation, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_controls", UserID: user, SpaceID: space.ID, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: "controls", RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	run := invocation.ID
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(user, "Control Mac", base64.RawURLEncoding.EncodeToString(key), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.AttachAIInvocationContext(ctx, user, run, space.ID, device.ID, "browser_tab", "scope-controls", "Control target", json.RawMessage(`["browser.inspect"]`), json.RawMessage(`{"kind":"browser_tab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(ctx, run, "vercel-workflow", "runtime-controls"); err != nil {
		t.Fatal(err)
	}
	queue := func(node string) *WorkflowDeviceNodeJob {
		t.Helper()
		job, err := database.QueueAIInvocationDeviceNodeJob(ctx, user, run, node, 1, "scope-controls", "browser.inspect", "browser.inspect", json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`))
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	claim := func() (*WorkflowDeviceNodeJob, string) {
		t.Helper()
		job, token, err := database.ClaimWorkflowDeviceNodeJob(user, device.ID, time.Minute, 2)
		if err != nil {
			t.Fatal(err)
		}
		return job, token
	}
	queued := queue("first")
	if queued.ControlVersion != 2 || time.Until(queued.DeadlineAt) > 5*time.Minute {
		t.Fatal("missing bounded execution contract")
	}
	if _, _, err := database.ClaimWorkflowDeviceNodeJob(user, device.ID, time.Minute, 1); !errors.Is(err, ErrAgentJobNotFound) {
		t.Fatalf("old host claimed v2: %v", err)
	}
	job, token := claim()
	if _, err := database.FinishWorkflowDeviceNodeJob(user, device.ID, job.ID, token, "completed", json.RawMessage(`{}`), ""); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("finished without begin: %v", err)
	}
	if _, err = database.Conn.Exec(`UPDATE workflow_device_node_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	retry, newToken := claim()
	if retry.ID != job.ID || token == newToken {
		t.Fatal("unstarted delivery was not retried with a fresh token")
	}
	if _, err := database.BeginWorkflowDeviceNodeJob(user, device.ID, job.ID, token); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("old token began execution: %v", err)
	}
	begun, err := database.BeginWorkflowDeviceNodeJob(user, device.ID, job.ID, newToken)
	if err != nil || begun.State != "executing" || begun.ExecutionStartedAt == nil {
		t.Fatalf("begin: %#v %v", begun, err)
	}
	if _, err = database.Conn.Exec(`UPDATE workflow_device_node_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ClaimWorkflowDeviceNodeJob(user, device.ID, time.Minute, 2); !errors.Is(err, ErrAgentJobNotFound) {
		t.Fatalf("possibly executed job repeated: %v", err)
	}
	uncertain, err := database.WorkflowDeviceNodeJob(ctx, user, job.ID)
	if err != nil || uncertain.State != "uncertain" {
		t.Fatalf("lost response not uncertain: %#v %v", uncertain, err)
	}
	result := json.RawMessage(`{"observed":true}`)
	for range 2 {
		if _, err := database.FinishWorkflowDeviceNodeJob(user, device.ID, job.ID, newToken, "completed", result, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.FinishWorkflowDeviceNodeJob(user, device.ID, job.ID, newToken, "failed", nil, "late_failure"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("overwrote confirmed outcome: %v", err)
	}
	queued = queue("stopped")
	stopped, err := database.StopWorkflowDeviceNodeJob(user, queued.ID)
	if err != nil || stopped.State != "canceled" {
		t.Fatalf("queued cancellation: %#v %v", stopped, err)
	}
	queue("active-stop")
	job, token = claim()
	if _, err = database.BeginWorkflowDeviceNodeJob(user, device.ID, job.ID, token); err != nil {
		t.Fatal(err)
	}
	stopped, err = database.StopWorkflowDeviceNodeJob(user, job.ID)
	if err != nil || stopped.State != "uncertain" {
		t.Fatalf("active cancellation: %#v %v", stopped, err)
	}
	if _, err := database.RenewWorkflowDeviceNodeJob(user, device.ID, job.ID, token); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("renewed canceled authority: %v", err)
	}
}

func TestUnstartedBrowserRecoveryPreservesJobAndRejectsStartedReplay(t *testing.T) {
	database, _, user, _ := sdkInvocationFixture(t)
	ctx := t.Context()
	space := createTestSpace(t, database, ctx, user, "Recovery")
	invocation, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_rearm", UserID: user, SpaceID: space.ID, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: "rearm", RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(user, "Recovery Mac", base64.RawURLEncoding.EncodeToString(key), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AttachAIInvocationContext(ctx, user, invocation.ID, space.ID, device.ID, "browser_tab", "scope-rearm", "Original view", json.RawMessage(`["browser.inspect"]`), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(ctx, invocation.ID, "vercel-workflow", "runtime-rearm"); err != nil {
		t.Fatal(err)
	}
	job, err := database.QueueAIInvocationDeviceNodeJob(ctx, user, invocation.ID, "inspect", 1, "scope-rearm", "browser.inspect", "browser.inspect", json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := database.StopWorkflowDeviceNodeJob(user, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	rearmed, err := database.RearmUnstartedBrowserJob(ctx, user, canceled)
	if err != nil || rearmed.ID != job.ID || rearmed.State != "queued" || rearmed.CancelRequestedAt != nil {
		t.Fatalf("unsafe rearm: %#v %v", rearmed, err)
	}
	claimed, token, err := database.ClaimWorkflowDeviceNodeJob(user, device.ID, time.Minute, 2)
	if err != nil || claimed.ID != job.ID {
		t.Fatalf("wrong recovery job: %#v %v", claimed, err)
	}
	if _, err := database.BeginWorkflowDeviceNodeJob(user, device.ID, job.ID, token); err != nil {
		t.Fatal(err)
	}
	unknown, err := database.StopWorkflowDeviceNodeJob(user, job.ID)
	if err != nil || unknown.State != "uncertain" {
		t.Fatalf("lost uncertain state: %#v %v", unknown, err)
	}
	if _, err := database.RearmUnstartedBrowserJob(ctx, user, unknown); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("started action was retried: %v", err)
	}

	waiting, err := database.AIInvocationDeviceWait(ctx, user, invocation.ID, "runtime-rearm", "expire-call", "expire-hook", "scope-rearm", "browser.inspect", strings.Repeat("a", 64), true)
	if err != nil || !waiting {
		t.Fatalf("admit wait: %v %v", waiting, err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET device_wait_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, invocation.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.QueueAgentDeviceResume(ctx, AgentDeviceWait{RunID: invocation.ID, HookToken: "expire-hook", Available: true}); err != nil {
		t.Fatal(err)
	}
	var available bool
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE((payload->>'available')::boolean,false) FROM agent_runtime_deliveries WHERE run_id=$1 AND operation='device.resume' AND payload->>'hook_token'='expire-hook'`, invocation.ID).Scan(&available)
	}); err != nil || available {
		t.Fatalf("expired wait resumed affirmatively: %v %v", available, err)
	}
	waiting, err = database.AIInvocationDeviceWait(ctx, user, invocation.ID, "runtime-rearm", "cancel-call", "cancel-hook", "scope-rearm", "browser.inspect", strings.Repeat("b", 64), true)
	if err != nil || !waiting {
		t.Fatalf("second wait: %v %v", waiting, err)
	}
	if _, err := database.CommitAIInvocationEvent(ctx, user, invocation.ID, "cancel-recovery", "invocation.canceled", json.RawMessage(`{"type":"invocation.canceled","state":"canceled"}`), "canceled"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueueAgentDeviceResume(ctx, AgentDeviceWait{RunID: invocation.ID, HookToken: "cancel-hook", Available: true}); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("canceled wait resumed: %v", err)
	}
}
