package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func TestAgentMembershipVersionApprovalAndTaskAssignmentRun(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Agent Space Owner", "agent-space-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Agent Space Member", "agent-space-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Agent teammate Space")
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
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Task Agent", Role: "Task specialist", Avatar: json.RawMessage(`{"kind":"preset","preset_id":"planner","accent":"blue"}`), Instructions: "Work visibly on assigned Tasks.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	spaceRole := "Launch planner"
	membership, err := database.AddSpaceAgentMembership(ctx, owner.ID, space.ID, SpaceAgentMembershipInput{AgentID: agent.ID, SpaceRole: &spaceRole})
	if err != nil {
		t.Fatal(err)
	}
	if membership.ApprovedVersion != 1 || !membership.Enabled || !agentMembershipHasPermission(membership, "attached_files.read") {
		t.Fatalf("initial membership = %#v", membership)
	}
	if membership.Role != "Task specialist" || membership.SpaceRole != spaceRole || membership.WorkState != "ready" || !strings.Contains(string(membership.Avatar), `"planner"`) {
		t.Fatalf("initial public identity = %#v", membership)
	}

	memberRoster, err := database.SpaceAgentMemberships(ctx, member.ID, space.ID)
	if err != nil || len(memberRoster) != 1 {
		t.Fatalf("member roster = %#v, %v", memberRoster, err)
	}
	if memberRoster[0].Instructions != "" || memberRoster[0].SpaceInstructions != "" {
		t.Fatalf("non-manager saw private Agent instructions: %#v", memberRoster[0])
	}

	updatedProfile := *agent
	updatedProfile.Instructions = "Use the new approved behavior."
	updatedProfile.Role = "Senior task specialist"
	updatedProfile.Avatar = json.RawMessage(`{"kind":"preset","preset_id":"builder","accent":"emerald"}`)
	updatedAgent, err := database.UpdatePersonalAgent(ctx, owner.ID, updatedProfile)
	if err != nil || updatedAgent.Version != 2 {
		t.Fatalf("profile update = %#v, %v", updatedAgent, err)
	}
	beforeApproval, err := database.SpaceAgentMembership(ctx, owner.ID, space.ID, agent.ID)
	if err != nil || !beforeApproval.UpdateAvailable || beforeApproval.ApprovedVersion != 1 {
		t.Fatalf("pinned membership before approval = %#v, %v", beforeApproval, err)
	}
	if beforeApproval.Role != "Task specialist" || !strings.Contains(string(beforeApproval.Avatar), `"planner"`) || beforeApproval.SpaceRole != spaceRole {
		t.Fatalf("Space did not retain pinned identity = %#v", beforeApproval)
	}
	if _, err := database.ApproveSpaceAgentVersion(ctx, member.ID, space.ID, agent.ID); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("approval without agents.manage = %v, want forbidden", err)
	}
	if err := database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionAgentsManage, "allow"); err != nil {
		t.Fatal(err)
	}
	approved, err := database.ApproveSpaceAgentVersion(ctx, member.ID, space.ID, agent.ID)
	if err != nil || approved.ApprovedVersion != 2 || approved.UpdateAvailable {
		t.Fatalf("approved membership = %#v, %v", approved, err)
	}
	if approved.Role != "Senior task specialist" || !strings.Contains(string(approved.Avatar), `"builder"`) || approved.SpaceRole != spaceRole {
		t.Fatalf("approved identity or Space role = %#v", approved)
	}

	task, err := database.CreateSpaceTask(ctx, member.ID, SpaceTask{
		SpaceID: space.ID, Title: "Review launch brief", Status: "todo", AssigneeAgentID: agent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "in_progress" {
		t.Fatalf("assigned task status = %q, want in_progress", task.Status)
	}
	taskContext, err := api.TestingResolvedCurrentTaskContext(ctx, database, member.ID, space.ID, task.ID)
	if err != nil || !strings.Contains(taskContext, task.TaskKey) || !strings.Contains(taskContext, task.Title) {
		t.Fatalf("current Task context = %q, %v", taskContext, err)
	}
	otherSpace, err := database.CreateSpace(ctx, owner.ID, "Other Agent Context Space")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.TestingResolvedCurrentTaskContext(ctx, database, owner.ID, otherSpace.ID, task.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-Space Task context error = %v, want not found", err)
	}
	type claimResult struct {
		run     *SpaceRun
		claimed bool
		err     error
	}
	startClaims := make(chan struct{})
	claimResults := make(chan claimResult, 8)
	for range 8 {
		go func() {
			<-startClaims
			run, claimed, err := database.ClaimAssignedAgentTaskRun(ctx, member.ID, *task)
			claimResults <- claimResult{run: run, claimed: claimed, err: err}
		}()
	}
	close(startClaims)
	var run *SpaceRun
	claimedCount := 0
	for range 8 {
		result := <-claimResults
		if result.err != nil {
			t.Fatalf("concurrent assignment claim error = %v", result.err)
		}
		if result.claimed {
			claimedCount++
			run = result.run
		}
	}
	if claimedCount != 1 || run == nil || run.BillingUserID != member.ID || run.SourceTaskID != task.ID {
		t.Fatalf("concurrent claims = %d, winning run = %#v", claimedCount, run)
	}
	if run.State != "queued" {
		t.Fatalf("new assignment run state = %q, want queued", run.State)
	}
	workingMembership, err := database.SpaceAgentMembership(ctx, member.ID, space.ID, agent.ID)
	if err != nil || workingMembership.WorkState != "working" || workingMembership.CurrentTaskID != task.ID {
		t.Fatalf("working roster summary = %#v, %v", workingMembership, err)
	}
	if _, claimedAgain, err := database.ClaimAssignedAgentTaskRun(ctx, member.ID, *task); err != nil || claimedAgain {
		t.Fatalf("duplicate assignment claim = %v, %v", claimedAgain, err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "contract-worker", 1, time.Minute)
	if err != nil || len(jobs) != 1 || jobs[0].Run.ID != run.ID || jobs[0].Attempt != 1 {
		t.Fatalf("leased assignment jobs = %#v, %v", jobs, err)
	}
	if active, err := database.RenewPersonalAgentTaskRunLease(ctx, run.ID, "contract-worker", time.Minute); err != nil || !active {
		t.Fatalf("renew assignment lease = %v, %v", active, err)
	}
	activity, err := database.SpaceTaskActivity(ctx, member.ID, space.ID, task.ID)
	if err != nil || len(activity) != 3 || activity[0].Kind != "assigned" || activity[1].Kind != "progress" || activity[2].Kind != "progress" {
		t.Fatalf("assignment activity = %#v, %v", activity, err)
	}
	if _, err := database.Conn.Exec(`UPDATE personal_agent_task_run_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE run_id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "recovery-worker", 1, time.Minute)
	if err != nil || len(recovered) != 1 || recovered[0].Attempt != 2 {
		t.Fatalf("recovered expired assignment lease = %#v, %v", recovered, err)
	}
	requeued, err := database.FailPersonalAgentTaskRunJob(ctx, run.ID, "recovery-worker", "provider_interrupted", "temporary provider interruption", true)
	if err != nil || !requeued {
		t.Fatalf("requeue interrupted assignment = %v, %v", requeued, err)
	}
	if state, attempt, err := database.PersonalAgentTaskRunJobState(ctx, run.ID); err != nil || state != "queued" || attempt != 2 {
		t.Fatalf("requeued assignment job = state:%q attempt:%d err:%v", state, attempt, err)
	}
	if _, err := database.Conn.Exec(`UPDATE personal_agent_task_run_jobs SET available_at=NOW()-INTERVAL '1 second' WHERE run_id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	finalLease, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "final-worker", 1, time.Minute)
	if err != nil || len(finalLease) != 1 || finalLease[0].Attempt != 3 {
		t.Fatalf("final assignment attempt = %#v, %v", finalLease, err)
	}

	task.AssigneeAgentID = ""
	unassigned, err := database.UpdateSpaceTask(ctx, member.ID, *task)
	if err != nil || unassigned.AssigneeAgentID != "" {
		t.Fatalf("unassigned task = %#v, %v", unassigned, err)
	}
	canceled, err := database.SpaceRun(ctx, member.ID, run.ID)
	if err != nil || canceled.State != "canceled" {
		t.Fatalf("assignment run after unassign = %#v, %v", canceled, err)
	}
	if active, err := database.RenewPersonalAgentTaskRunLease(ctx, run.ID, "final-worker", time.Minute); err != nil || active {
		t.Fatalf("canceled assignment lease = %v, %v", active, err)
	}
}

func agentMembershipHasPermission(membership *SpaceAgentMembership, permission string) bool {
	var permissions map[string]bool
	return json.Unmarshal(membership.Permissions, &permissions) == nil && permissions[permission]
}

func TestSpaceAgentMessageSendRechecksMembership(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Global Agent Owner", "global-agent-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := database.CreateSpace(ctx, owner.ID, "Visible Space")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Messenger", Instructions: "Help with Space communication.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	membership, err := database.AddSpaceAgentMembership(ctx, owner.ID, visible.ID, SpaceAgentMembershipInput{AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	renamed := *agent
	renamed.Name = "Unapproved Messenger Name"
	if _, err := database.UpdatePersonalAgent(ctx, owner.ID, renamed); err != nil {
		t.Fatal(err)
	}
	message, err := database.CreatePersonalAgentSpaceMessage(ctx, owner.ID, visible.ID, agent.ID, "Launch is today")
	if err != nil || message.SenderKind != "agent" || message.SenderAgentID != agent.ID || message.SenderUserID != owner.ID || message.SenderName != "Messenger" {
		t.Fatalf("Agent message = %#v, %v", message, err)
	}
	history, err := database.SpaceMessages(ctx, owner.ID, visible.ID, 0, 10)
	if err != nil || len(history) != 1 || history[0].SenderName != "Messenger" {
		t.Fatalf("Agent history attribution = %#v, %v", history, err)
	}
	enabled := false
	if _, err := database.UpdateSpaceAgentMembership(ctx, owner.ID, visible.ID, agent.ID, SpaceAgentMembershipInput{
		Enabled: &enabled, SpaceInstructions: membership.SpaceInstructions,
		Permissions: membership.Permissions, MembershipVersion: membership.MembershipVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreatePersonalAgentSpaceMessage(ctx, owner.ID, visible.ID, agent.ID, "Must not send"); !errors.Is(err, ErrPersonalAgentNotFound) {
		t.Fatalf("send after membership disable = %v, want not found", err)
	}
}

func TestSpaceAgentDelegationTargetsOnlyEnabledSpaceMembers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Misty Routing Owner", "misty-routing-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Misty Routing Member", "misty-routing-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Misty Routing Space")
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
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Routing Specialist", Role: "Researcher", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplacePersonalAgentGrants(ctx, owner.ID, agent.ID, []PersonalAgentGrantInput{{
		SpaceID: space.ID, AllMembers: false, MemberUserIDs: []string{owner.ID},
	}}); err != nil {
		t.Fatal(err)
	}

	resolved, _, err := api.TestingResolveSpaceDelegationTarget(ctx, database, owner.ID, space.ID, agent.ID, "", "Delegate this to Routing Specialist")
	if err != nil || resolved == nil || resolved.AgentID != agent.ID {
		t.Fatalf("owner delegation target = %#v, %v", resolved, err)
	}
	memberResolved, _, err := api.TestingResolveSpaceDelegationTarget(ctx, database, member.ID, space.ID, agent.ID, "", "Delegate this to Routing Specialist")
	if err != nil || memberResolved == nil || memberResolved.AgentID != agent.ID {
		t.Fatalf("Space member delegation target = %#v, %v", memberResolved, err)
	}

	membership, err := database.SpaceAgentMembership(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := database.UpdateSpaceAgentMembership(ctx, owner.ID, space.ID, agent.ID, SpaceAgentMembershipInput{
		Enabled: &disabled, SpaceInstructions: membership.SpaceInstructions,
		Permissions: membership.Permissions, MembershipVersion: membership.MembershipVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.TestingResolveSpaceDelegationTarget(ctx, database, owner.ID, space.ID, agent.ID, "", "Delegate this to Routing Specialist"); !errors.Is(err, workflowv2.ErrCapabilityDenied) {
		t.Fatalf("disabled Agent delegation error = %v, want capability denied", err)
	}
}
