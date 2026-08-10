package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestPersonalAgentOwnershipSpaceMembershipAndConversationIsolation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Agent Owner", "personal-agent-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Agent Member", "personal-agent-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Agent Space")
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
	privateMember, err := database.CreateUser("Private Member", "personal-agent-private@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	privateInvite, err := database.InviteToSpace(ctx, owner.ID, space.ID, privateMember.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, privateMember.ID, privateInvite.ID, true); err != nil {
		t.Fatal(err)
	}
	privateConversation, err := database.CreateSpaceConversation(ctx, owner.ID, space.ID, "Private group", []SpaceActorRef{{Kind: "person", UserID: privateMember.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateSpaceConversationMessageWithReferences(ctx, owner.ID, space.ID, privateConversation.ID, []MessageSpan{{Type: "text", Text: "private group secret"}}, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	memberContext, err := database.PersonalAgentSpaceContext(ctx, member.ID, space.ID, []byte(`{"space_chat":true}`))
	if err != nil || strings.Contains(memberContext, "private group secret") {
		t.Fatalf("Space-wide context leaked selected-member chat: %q, %v", memberContext, err)
	}
	privateContext, err := database.PersonalAgentSpaceContextForConversation(ctx, owner.ID, space.ID, privateConversation.ID, []byte(`{"space_chat":true}`))
	if err != nil || !strings.Contains(privateContext, "private group secret") {
		t.Fatalf("selected-member context = %q, %v", privateContext, err)
	}

	created, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Researcher", Description: "Finds relevant material", Instructions: "Prefer primary sources.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.OwnerUserID != owner.ID || !created.Enabled || created.Version != 1 {
		t.Fatalf("created Agent = %#v", created)
	}
	if agents, err := database.AccessiblePersonalAgents(ctx, member.ID, space.ID); err != nil || len(agents) != 0 {
		t.Fatalf("private Agent visible to member: %#v, %v", agents, err)
	}

	grants, err := database.ReplacePersonalAgentGrants(ctx, owner.ID, created.ID, []PersonalAgentGrantInput{{
		SpaceID: space.ID, MemberUserIDs: []string{member.ID},
	}})
	if err != nil || len(grants) != 1 || !grants[0].AllMembers || len(grants[0].MemberUserIDs) != 0 {
		t.Fatalf("grants = %#v, %v", grants, err)
	}
	memberAgents, err := database.AccessiblePersonalAgents(ctx, member.ID, space.ID)
	if err != nil || len(memberAgents) != 1 {
		t.Fatalf("shared Agents = %#v, %v", memberAgents, err)
	}
	if memberAgents[0].Instructions != "" || len(memberAgents[0].ContextPermissions) != 0 || len(memberAgents[0].ToolPermissions) != 0 {
		t.Fatalf("shared Agent exposed private configuration: %#v", memberAgents[0])
	}
	privateMemberAgents, err := database.AccessiblePersonalAgents(ctx, privateMember.ID, space.ID)
	if err != nil || len(privateMemberAgents) != 1 {
		t.Fatalf("Space member could not see first-class Agent membership: %#v, %v", privateMemberAgents, err)
	}
	effectiveContext, err := database.EffectivePersonalAgentContextPermissions(ctx, member.ID, space.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var effective map[string]bool
	if json.Unmarshal(effectiveContext, &effective) != nil || !effective["members"] || !effective["library"] || !effective["tasks"] {
		t.Fatalf("effective Agent context did not preserve configured readable sections: %s", effectiveContext)
	}
	snapshot, err := database.PersonalAgentSpaceContext(ctx, member.ID, space.ID, effectiveContext)
	if err != nil || !strings.Contains(snapshot, "Members:") || !strings.Contains(snapshot, "Agent Owner (owner)") {
		t.Fatalf("Agent context omitted permitted Space members: %q, %v", snapshot, err)
	}
	membership, err := database.SpaceAgentMembership(ctx, owner.ID, space.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := database.UpdateSpaceAgentMembership(ctx, owner.ID, space.ID, created.ID, SpaceAgentMembershipInput{
		Enabled: &enabled, MembershipVersion: membership.MembershipVersion,
		Permissions: json.RawMessage(`{"messages.read":true,"messages.write":true,"tasks.view":false,"tasks.manage":false,"attached_files.read":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	effectiveContext, err = database.EffectivePersonalAgentContextPermissions(ctx, member.ID, space.ID, created.ID)
	if err != nil || json.Unmarshal(effectiveContext, &effective) != nil || effective["tasks"] || effective["task_notes"] || effective["notes"] {
		t.Fatalf("Agent membership did not remove Task context: %s, %v", effectiveContext, err)
	}

	updated := *created
	updated.Name = "Research Guide"
	updatedAgent, err := database.UpdatePersonalAgent(ctx, owner.ID, updated)
	if err != nil || updatedAgent.Name != "Research Guide" || updatedAgent.Version != 2 {
		t.Fatalf("updated Agent = %#v, %v", updatedAgent, err)
	}
	if _, err := database.UpdatePersonalAgent(ctx, owner.ID, *created); !errors.Is(err, ErrPersonalAgentConflict) {
		t.Fatalf("stale update = %v, want ErrPersonalAgentConflict", err)
	}

	if _, err := database.ReplacePersonalAgentGrants(ctx, owner.ID, created.ID, nil); err != nil {
		t.Fatal(err)
	}
	if agents, err := database.AccessiblePersonalAgents(ctx, member.ID, space.ID); err != nil || len(agents) != 0 {
		t.Fatalf("removed Space Agent membership remained visible: %#v, %v", agents, err)
	}
	if err := database.DeletePersonalAgent(ctx, owner.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PersonalAgentByID(ctx, owner.ID, created.ID); !errors.Is(err, ErrPersonalAgentNotFound) {
		t.Fatalf("deleted Agent lookup = %v, want ErrPersonalAgentNotFound", err)
	}
}
