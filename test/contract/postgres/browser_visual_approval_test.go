package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestBrowserApprovalUsesLatestCompletedInspectionIncludingVisual(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Visual test", "visual-test@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Visual")
	agent, err := database.SavePersonalAgent(ctx, owner.ID, "", AgentProfileInput{Name: "Visual", Role: "Inspect", ModelMode: "automatic", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	lease := AgentExecutionLease{AgentID: agent.ID, SpaceID: space.ID, TaskID: "visual-task", WindowLabel: "main"}
	if err = database.AcquireAgentExecution(ctx, owner.ID, lease); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"agent_id": agent.ID, "execution_mode": "agent", "task_id": lease.TaskID, "window_label": lease.WindowLabel})
	run, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_visual_review", UserID: owner.ID, SpaceID: space.ID, SurfaceID: "global", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: "visual-review", RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(owner.ID, "Visual Mac", base64.RawURLEncoding.EncodeToString(key), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"visual-source", "other-source"} {
		_, err = database.AttachAIInvocationContext(ctx, owner.ID, run.ID, space.ID, device.ID, "browser_tab", scope, scope, json.RawMessage(`["browser.inspect","browser.visual","browser.interact","browser.workspace.visual","browser.workspace.interact"]`), json.RawMessage(`{"app_id":"browser","window_label":"main","workspace_control":true}`))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = database.ActivateAIInvocationRuntime(ctx, run.ID, "vercel-workflow", "visual-runtime"); err != nil {
		t.Fatal(err)
	}
	complete := func(node, scope, operation, state string) {
		t.Helper()
		job, err := database.QueueAIInvocationDeviceNodeJob(ctx, owner.ID, run.ID, node, 1, scope, operation, operation, json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`))
		if err != nil {
			t.Fatal(err)
		}
		claimed, token, err := database.ClaimWorkflowDeviceNodeJob(owner.ID, device.ID, time.Minute, 2)
		if err != nil || claimed == nil || claimed.ID != job.ID {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		if _, err = database.BeginWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token); err != nil {
			t.Fatal(err)
		}
		output, _ := json.Marshal(map[string]string{"documentId": node, "url": "https://example.com"})
		code := ""
		if state == "failed" {
			code = "capture_failed"
		}
		if _, err = database.FinishWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token, state, output, code); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		target, err := database.BrowserToolApprovalTarget(ctx, owner.ID, run.ID, "visual-runtime", "visual-source", "browser.interact")
		if err != nil {
			t.Fatal(err)
		}
		var page map[string]string
		if err = json.Unmarshal(target.Snapshot, &page); err != nil {
			t.Fatal(err)
		}
		if page["documentId"] != want {
			t.Fatalf("reviewed %q, want %q", page["documentId"], want)
		}
	}
	complete("semantic", "visual-source", "browser.inspect", "completed")
	check("semantic")
	complete("visual", "visual-source", "browser.visual", "completed")
	check("visual")
	complete("failed-visual", "visual-source", "browser.visual", "failed")
	check("visual")
	complete("other", "other-source", "browser.visual", "completed")
	check("visual")
	complete("semantic-later", "visual-source", "browser.inspect", "completed")
	check("semantic-later")
	if _, err := database.BrowserToolApprovalTarget(ctx, owner.ID, run.ID, "wrong-runtime", "visual-source", "browser.interact"); err == nil {
		t.Fatal("accepted another runtime")
	}
	complete("workspace-visual", "visual-source", "browser.workspace.visual", "completed")
	target, err := database.BrowserToolApprovalTarget(ctx, owner.ID, run.ID, "visual-runtime", "visual-source", "browser.workspace.interact")
	if err != nil {
		t.Fatal(err)
	}
	review := ProtectedSDKApproval{EffectID: uuid.NewString(), Digest: strings.Repeat("a", 64), Ciphertext: []byte(strings.Repeat("protected", 8))}
	approval, allowed, err := database.RequireBrowserToolApproval(ctx, owner.ID, run.ID, "visual-runtime", "workspace-enter", "browser.workspace.interact", "exact-arguments", "review-hook", "Press Enter in the address bar", *target, review)
	if err != nil || allowed || approval == nil || approval.State != "pending" {
		t.Fatalf("workspace review admission: %v %v %v", approval, allowed, err)
	}
	stored, protected, err := database.BrowserToolApprovalByCall(ctx, owner.ID, run.ID, "workspace-enter")
	if err != nil || stored.ID != approval.ID || protected.Digest != review.Digest {
		t.Fatalf("workspace review recovery: %v", err)
	}
	if err := database.DecideSDKToolApproval(ctx, owner.ID, run.ID, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	_, allowed, err = database.RequireBrowserToolApproval(ctx, owner.ID, run.ID, "visual-runtime", "workspace-enter", "browser.workspace.interact", "exact-arguments", "review-hook", "Press Enter in the address bar", *target, review)
	if err != nil || !allowed {
		t.Fatalf("approved workspace action did not resume: allowed=%v err=%v", allowed, err)
	}
	if _, _, err := database.RequireBrowserToolApproval(ctx, owner.ID, run.ID, "visual-runtime", "workspace-enter", "browser.workspace.interact", "changed-arguments", "review-hook", "Changed action", *target, review); err == nil {
		t.Fatal("approval accepted changed arguments")
	}
}
