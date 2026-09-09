package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestCreatorToolApprovalIsExactOwnerScopedAndRecoverable(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Approval Owner", "creator-approval@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	intruder, err := database.CreateUser("Approval Intruder", "creator-approval-intruder@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Approval Space")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Send the update", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "approval-worker", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim = %#v, %v", jobs, err)
	}
	run, err = database.MarkPersonalAgentTaskRunDispatched(ctx, run.ID, "approval-worker", "vercel-workflow", "workflow-approval")
	if err != nil {
		t.Fatal(err)
	}
	approval, allowed, err := database.RequireCreatorToolApproval(ctx, run, "call-1", "messages.send", "consequential", "args-hash", "signed-call", "hook-token", "Send an update")
	if err != nil || allowed || approval.State != "pending" {
		t.Fatalf("approval = %#v, allowed=%v, err=%v", approval, allowed, err)
	}
	if _, err := database.DecideCreatorToolApproval(ctx, intruder.ID, run.ID, approval.ID, true); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("intruder decision = %v", err)
	}
	decided, err := database.DecideCreatorToolApproval(ctx, owner.ID, run.ID, approval.ID, true)
	if err != nil || decided.State != "approved" {
		t.Fatalf("creator decision = %#v, %v", decided, err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(ctx, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].Operation != "approval.resume" {
		t.Fatalf("approval decision did not atomically queue continuation: %#v %v", deliveries, err)
	}
	pending, err := database.CreatorToolApprovalResumesPending(ctx, 20)
	if err != nil || len(pending) != 1 || pending[0].ID != approval.ID {
		t.Fatalf("pending resumes = %#v, %v", pending, err)
	}
	if err := database.MarkCreatorToolApprovalResumed(ctx, run.ID, approval.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkCreatorToolApprovalResumed(ctx, run.ID, approval.ID); err != nil {
		t.Fatalf("duplicate runtime acknowledgement must be idempotent: %v", err)
	}
	run.EffectiveRunMode = "ask"
	_, allowed, err = database.RequireCreatorToolApproval(ctx, run, "call-1", "messages.send", "consequential", "args-hash", "signed-call", "hook-token", "Send an update")
	if err != nil || !allowed {
		t.Fatalf("approved exact replay allowed=%v, err=%v", allowed, err)
	}
	if _, _, err := database.RequireCreatorToolApproval(ctx, run, "call-1", "messages.send", "consequential", "different-hash", "signed-call", "hook-token", "Send an update"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("modified arguments = %v", err)
	}
	detail, err := database.PersonalAgentRunDetailForOwner(ctx, owner.ID, run.ID)
	if err != nil || detail.Summary.EffectiveRunMode != "ask" || detail.Summary.Phase != "working" {
		t.Fatalf("run detail = %#v, %v", detail, err)
	}
	next, allowed, err := database.RequireCreatorToolApproval(ctx, run, "call-2", "messages.send", "consequential", "next-hash", "next-signature", "next-hook", "Send another message")
	if err != nil || allowed || next.State != "pending" {
		t.Fatalf("one approval authorized another action: %#v %v", next, err)
	}
}

func TestCreatorRunCancelsWhenSpaceMembershipIsRevoked(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Space Owner", "revoke-run-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	creator, err := database.CreateUser("Agent Creator", "revoke-run-creator@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Revocation Space")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, creator.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, creator.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	agent, err := database.EnsureAskIdentity(ctx, creator.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, creator.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Work here"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RemoveSpaceMember(ctx, owner.ID, space.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	state, _, err := database.PersonalAgentTaskRunJobState(ctx, run.ID)
	if err != nil || state != "canceled" {
		t.Fatalf("job state after membership revocation = %q, %v", state, err)
	}
}
