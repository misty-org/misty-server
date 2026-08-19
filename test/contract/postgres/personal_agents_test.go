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

	ownerAgents, err := database.AccessiblePersonalAgents(ctx, owner.ID, space.ID)
	if err != nil || len(ownerAgents) != 1 || ownerAgents[0].DefaultRunMode != "auto" {
		t.Fatalf("creator Agents = %#v, %v", ownerAgents, err)
	}
	memberAgents, err := database.AccessiblePersonalAgents(ctx, member.ID, space.ID)
	if err != nil || len(memberAgents) != 0 {
		t.Fatalf("another member discovered an unused Agent: %#v, %v", memberAgents, err)
	}
	privateMemberAgents, err := database.AccessiblePersonalAgents(ctx, privateMember.ID, space.ID)
	if err != nil || len(privateMemberAgents) != 0 {
		t.Fatalf("another member discovered an unused Agent: %#v, %v", privateMemberAgents, err)
	}
	effectiveContext, err := database.EffectivePersonalAgentContextPermissions(ctx, owner.ID, space.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var effective map[string]bool
	if json.Unmarshal(effectiveContext, &effective) != nil || !effective["members"] || !effective["library"] || !effective["tasks"] {
		t.Fatalf("effective Agent context did not preserve configured readable sections: %s", effectiveContext)
	}
	snapshot, err := database.PersonalAgentSpaceContext(ctx, owner.ID, space.ID, effectiveContext)
	if err != nil || !strings.Contains(snapshot, "Members:") || !strings.Contains(snapshot, "Agent Owner (owner)") {
		t.Fatalf("Agent context omitted permitted Space members: %q, %v", snapshot, err)
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

	if agents, err := database.AccessiblePersonalAgents(ctx, member.ID, space.ID); err != nil || len(agents) != 0 {
		t.Fatalf("creator-only Agent became visible: %#v, %v", agents, err)
	}
	if err := database.DeletePersonalAgent(ctx, owner.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PersonalAgentByID(ctx, owner.ID, created.ID); !errors.Is(err, ErrPersonalAgentNotFound) {
		t.Fatalf("deleted Agent lookup = %v, want ErrPersonalAgentNotFound", err)
	}
}
