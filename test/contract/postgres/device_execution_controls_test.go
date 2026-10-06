package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestDeviceExecutionControls(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Device owner", "device-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	user := owner.ID
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
		if job.SpaceID != "" {
			t.Fatalf("account-owned device claim acquired a Space owner: %q", job.SpaceID)
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
	if err != nil || begun.SpaceID != "" || begun.State != "executing" || begun.ExecutionStartedAt == nil {
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
