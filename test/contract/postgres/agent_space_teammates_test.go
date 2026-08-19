package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestCreatorAgentIsAutomaticAndCreatorControlled(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Companion Owner", "companion-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Companion Member", "companion-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Companion Space")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{Name: "Space Pal", Instructions: "Work visibly.", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
	if err != nil {
		t.Fatal(err)
	}
	ownerAgents, err := database.SpaceAgentMemberships(ctx, owner.ID, space.ID)
	if err != nil || len(ownerAgents) != 1 || !ownerAgents[0].CanControl || ownerAgents[0].DefaultRunMode != "auto" {
		t.Fatalf("owner roster = %#v, %v", ownerAgents, err)
	}
	memberAgents, err := database.SpaceAgentMemberships(ctx, member.ID, space.ID)
	if err != nil || len(memberAgents) != 0 {
		t.Fatalf("unused Agent leaked = %#v, %v", memberAgents, err)
	}
	if _, err := database.CreateCreatorAgentRun(ctx, member.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Do work"}); !errors.Is(err, ErrPersonalAgentNotFound) {
		t.Fatalf("non-creator run = %v", err)
	}
	if _, err := database.CreateSpaceTask(ctx, member.ID, SpaceTask{SpaceID: space.ID, Title: "Forbidden assignment", Status: "todo", AssigneeAgentID: agent.ID}); err == nil {
		t.Fatalf("non-creator assignment = %v", err)
	}
	task, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{SpaceID: space.ID, Title: "Creator assignment", Status: "todo", AssigneeAgentID: agent.ID})
	if err != nil || task.Status != "in_progress" {
		t.Fatalf("creator assignment = %#v, %v", task, err)
	}
	memberAgents, err = database.SpaceAgentMemberships(ctx, member.ID, space.ID)
	if err != nil || len(memberAgents) != 1 || memberAgents[0].CanControl || memberAgents[0].Instructions != "" {
		t.Fatalf("public referenced identity = %#v, %v", memberAgents, err)
	}
	if _, mentions, err := database.CreateSpaceMessageWithReferencesAndClientNonce(ctx, member.ID, space.ID, []MessageSpan{
		{Type: "mention", AgentID: agent.ID, Label: agent.Name}, {Type: "text", Text: " shared context only"},
	}, nil, nil, nil, "", ""); err != nil || len(mentions) != 1 {
		t.Fatalf("non-creator public mention should remain a normal message: mentions=%v err=%v", mentions, err)
	}
}

func TestCreatorRunModesAndBoundedDelegation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Delegation Owner", "creator-delegation@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Delegation Space")
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{Name: "Lead", DefaultRunMode: "full", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{Name: "Helper", DefaultRunMode: "auto", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, first.ID, CreatorAgentRunInput{Instruction: "Lead this work", Mode: "full"})
	if err != nil || parent.InitialRunMode != "full" || parent.OwnerUserID != owner.ID {
		t.Fatalf("parent = %#v, %v", parent, err)
	}
	child, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, second.ID, CreatorAgentRunInput{Instruction: "Help", Mode: "full", ParentRunID: parent.ID})
	if err != nil || child.InitialRunMode != "auto" || child.ParentRunID != parent.ID || child.DelegationDepth != 1 {
		t.Fatalf("child = %#v, %v", child, err)
	}
	if _, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, first.ID, CreatorAgentRunInput{Instruction: "Self delegate", ParentRunID: parent.ID}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("self delegation = %v", err)
	}
}

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
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{Name: "Approver", DefaultRunMode: "ask", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
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
	run.EffectiveRunMode = "full"
	_, allowed, err = database.RequireCreatorToolApproval(ctx, run, "call-1", "messages.send", "consequential", "args-hash", "signed-call", "hook-token", "Send an update")
	if err != nil || !allowed {
		t.Fatalf("approved exact replay allowed=%v, err=%v", allowed, err)
	}
	if _, _, err := database.RequireCreatorToolApproval(ctx, run, "call-1", "messages.send", "consequential", "different-hash", "signed-call", "hook-token", "Send an update"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("modified arguments = %v", err)
	}
	detail, err := database.PersonalAgentRunDetailForOwner(ctx, owner.ID, run.ID)
	if err != nil || detail.Summary.EffectiveRunMode != "full" || detail.Summary.Phase != "working" {
		t.Fatalf("run detail = %#v, %v", detail, err)
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
	agent, err := database.CreatePersonalAgent(ctx, creator.ID, PersonalAgent{Name: "Revoked companion", DefaultRunMode: "auto", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
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
