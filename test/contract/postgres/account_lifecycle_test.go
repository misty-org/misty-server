package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAccountDeletionBlocksOwnersAndAnonymizesMembersAfterRetention(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser(
		"Deletion Owner", "deletion-owner@example.com", "password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser(
		"Deletion Member", "deletion-member@example.com", "password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Deletion Space")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(
		ctx, owner.ID, space.ID, member.Email,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionAgentsManage, "allow"); err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreatePersonalAgent(ctx, member.ID, PersonalAgent{
		Name: "Private deletion Agent", Instructions: "Sensitive owner instructions",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddSpaceAgentMembership(ctx, member.ID, space.ID, SpaceAgentMembershipInput{AgentID: agent.ID}); err != nil {
		t.Fatal(err)
	}
	if err := database.AppendPersonalAgentMemory(ctx, member.ID, space.ID, agent.ID, "private prompt", "private response"); err != nil {
		t.Fatal(err)
	}
	conversationID := "conversation_87654321-4321-4321-4321-210987654321"
	if err := database.CreateAgentSession(ctx, conversationID, member.ID, []byte(`{"messages":[{"text":"private"}]}`), time.Now().Add(time.Hour), time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	blockers, err := database.AccountDeletionBlockers(ctx, owner.ID)
	if err != nil || len(blockers) != 1 || blockers[0].SpaceID != space.ID {
		t.Fatalf("owner blockers = %#v, %v", blockers, err)
	}
	if _, err := database.BeginAccountDeletion(
		ctx, owner.ID, "deletion_owner_blocked", strings.Repeat("a", 64),
		30*24*time.Hour,
	); !errors.Is(err, ErrAccountDeletionBlocked) {
		t.Fatalf("owner deletion error = %v", err)
	}

	if err := database.CreateSession(strings.Repeat("b", 64), member.ID); err != nil {
		t.Fatal(err)
	}
	request, err := database.BeginAccountDeletion(
		ctx, member.ID, "deletion_member_request", strings.Repeat("c", 64),
		30*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != "processing" {
		t.Fatalf("request = %#v", request)
	}
	if sessionUser, err := database.GetSessionUserID(strings.Repeat("b", 64)); err != nil ||
		sessionUser != "" {
		t.Fatalf("revoked session = %q, %v", sessionUser, err)
	}
	if user, err := database.GetUserByID(member.ID); err != nil || user != nil {
		t.Fatalf("pending user remained login-visible = %#v, %v", user, err)
	}
	var agentEnabled, membershipEnabled bool
	if err := database.Conn.QueryRow(`SELECT enabled FROM personal_agents WHERE id=$1`, agent.ID).Scan(&agentEnabled); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn.QueryRow(`SELECT enabled FROM personal_agent_space_grants WHERE agent_id=$1`, agent.ID).Scan(&membershipEnabled); err != nil {
		t.Fatal(err)
	}
	if agentEnabled || membershipEnabled {
		t.Fatal("pending deletion left owned Agent invokable")
	}
	if err := database.ScheduleAccountDeletion(
		ctx, request.ID, map[string]string{"drive:test": "revoked"},
	); err != nil {
		t.Fatal(err)
	}
	var stillMember bool
	if err := database.Conn.QueryRow(`
		SELECT EXISTS(
		    SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2
		)`, space.ID, member.ID,
	).Scan(&stillMember); err != nil {
		t.Fatal(err)
	}
	if stillMember {
		t.Fatal("scheduled deletion retained Space membership")
	}
	if _, err := database.Conn.Exec(`
		UPDATE account_deletion_requests
		SET purge_after=NOW()-INTERVAL '1 minute'
		WHERE id=$1`, request.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteAccountDeletion(ctx, request.ID); err != nil {
		t.Fatal(err)
	}
	var state, name, email string
	if err := database.Conn.QueryRow(`
		SELECT lifecycle_state,name,email FROM users WHERE id=$1`, member.ID,
	).Scan(&state, &name, &email); err != nil {
		t.Fatal(err)
	}
	if state != "deleted" || name != "Deleted user" ||
		!strings.HasSuffix(email, "@misty.invalid") {
		t.Fatalf("anonymized user = state:%q name:%q email:%q", state, name, email)
	}
	var agentName, instructions, versionInstructions string
	if err := database.Conn.QueryRow(`SELECT name,instructions FROM personal_agents WHERE id=$1`, agent.ID).Scan(&agentName, &instructions); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn.QueryRow(`SELECT instructions FROM personal_agent_versions WHERE agent_id=$1 LIMIT 1`, agent.ID).Scan(&versionInstructions); err != nil {
		t.Fatal(err)
	}
	var privateRows int
	if err := database.Conn.QueryRow(`SELECT
		(SELECT COUNT(*) FROM agent_conversations WHERE user_id=$1)+
		(SELECT COUNT(*) FROM personal_agent_instances WHERE invoker_user_id=$1 OR agent_id=$2)`, member.ID, agent.ID).Scan(&privateRows); err != nil {
		t.Fatal(err)
	}
	if agentName != "Deleted Agent" || instructions != "" || versionInstructions != "" || privateRows != 0 {
		t.Fatalf("Agent deletion redaction failed: name=%q instructions=%q version=%q private_rows=%d", agentName, instructions, versionInstructions, privateRows)
	}
	status, err := database.AccountDeletionStatus(
		ctx, request.ID, strings.Repeat("c", 64),
	)
	if err != nil || status.Status != "completed" || status.CompletedAt == nil {
		t.Fatalf("completed status = %#v, %v", status, err)
	}
}
