package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/db"
)

func TestAgentsAreMembersUnderTheirOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	alice, err := database.CreateUser("alice-agent-home@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob-agent-home@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	home := personalSpace(t, database, ctx, alice.ID)
	if home.Name != alice.Username+"'s Space" {
		t.Fatalf("personal Space name = %q", home.Name)
	}
	if _, err := database.EnsureAskIdentity(ctx, alice.ID, "google/gemini-2.5-flash-lite"); err != nil {
		t.Fatal(err)
	}
	writer, err := database.SavePersonalAgent(ctx, alice.ID, "", AgentProfileInput{Name: "Writer", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := database.SpaceAgents(ctx, alice.ID, home.ID)
	if err != nil || len(agents) != 2 {
		t.Fatalf("agents in the personal Space = %#v, %v, want Misty and Writer", agents, err)
	}

	shared := createTestSpace(t, database, ctx, bob.ID, "Studio")
	if _, err := database.AddSpaceAgent(ctx, alice.ID, shared.ID, writer.ID); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("bringing an agent into a Space its owner isn't in = %v", err)
	}
	invite, err := database.TestingInviteToSpace(ctx, bob.ID, shared.ID, alice.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, alice.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddSpaceAgent(ctx, alice.ID, shared.ID, writer.ID); err != nil {
		t.Fatal(err)
	}
	agents, err = database.SpaceAgents(ctx, bob.ID, shared.ID)
	if err != nil || len(agents) != 1 || agents[0].AgentID != writer.ID || agents[0].OwnerName != alice.Username {
		t.Fatalf("Studio's agents = %#v, %v", agents, err)
	}
	// An agent can be in many Spaces at once.
	if ids, err := database.AgentSpaceIDs(ctx, alice.ID, writer.ID); err != nil || len(ids) != 2 || ids[0] != home.ID {
		t.Fatalf("Writer's Spaces = %v, %v", ids, err)
	}
	// Only its owner or the Space's owner can take it out.
	carol, err := database.CreateUser("carol-agent-home@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	addTestSpaceMember(t, database, shared.ID, carol.ID)
	if err := database.RemoveSpaceAgent(ctx, carol.ID, shared.ID, writer.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member removing Alice's agent = %v", err)
	}
	if err := database.RemoveSpaceAgent(ctx, alice.ID, shared.ID, writer.ID); err != nil {
		t.Fatal(err)
	}

	if err := database.DeletePersonalAgent(ctx, alice.ID, writer.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := database.AgentSpaceIDs(ctx, alice.ID, writer.ID); err != nil || len(ids) != 0 {
		t.Fatalf("a deleted agent is still in %v, %v", ids, err)
	}
}

func TestMentioningAnAgentInSpaceChatAsksItsOwner(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	mention := []MessageSpan{{Type: "text", Text: "Can you check this? "}, {Type: "mention", AgentID: f.bobAgent.ID, Label: "Researcher"}}
	message, mentioned, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, mention, nil)
	if err != nil || len(mentioned) != 1 || mentioned[0] != f.bobAgent.ID {
		t.Fatalf("message mentioning another member's agent = %v, %v", mentioned, err)
	}
	ask := AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.alice.ID, RequesterRunID: MentionRequester(message.ID),
		TargetAgentID: f.bobAgent.ID, Message: "Can you check this? @Researcher", IdempotencyKey: f.bobAgent.ID}
	request, err := f.database.CreateAgentMemberRequest(ctx, ask)
	if err != nil || request.RunState != "queued" || request.SourceMessageID != message.ID {
		t.Fatalf("mention request = %#v, %v", request, err)
	}
	child, err := f.database.SpaceRun(ctx, f.bob.ID, request.ChildRunID)
	if err != nil || child.ModelUserID != f.alice.ID || child.OwnerUserID != f.bob.ID {
		t.Fatalf("mention work = %#v, %v, want Bob's agent billed to Alice", child, err)
	}
	if replay, err := f.database.CreateAgentMemberRequest(ctx, ask); err != nil || replay.ID != request.ID {
		t.Fatalf("the same mention asked twice = %#v, %v", replay, err)
	}
	reply, err := f.database.CreateAgentMemberRequestMessage(ctx, request, "agent_member_reply", "The launch is on the 14th.")
	if err != nil || reply.SenderAgentID != f.bobAgent.ID || reply.ReplyToMessageID != message.ID {
		t.Fatalf("reply = %#v, %v, want Bob's agent answering under the message", reply, err)
	}

	// Bob's agent @mentions Alice's agent in its reply: Alice's agent picks up
	// the thread, still billed to Alice, one hop deep.
	if _, err := f.database.AddSpaceAgent(ctx, f.alice.ID, f.space.ID, f.aliceAgent.ID); err != nil {
		t.Fatal(err)
	}
	handoff := AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.alice.ID, RequesterAgentID: f.bobAgent.ID,
		RequesterRunID: ReplyRequester(reply.ID), TargetAgentID: f.aliceAgent.ID, Message: "@Planner can you add it to the plan?", IdempotencyKey: f.aliceAgent.ID}
	next, err := f.database.CreateAgentMemberRequest(ctx, handoff)
	if err != nil || next.SourceMessageID != reply.ID || next.RunState != "queued" {
		t.Fatalf("agent-to-agent request = %#v, %v", next, err)
	}
	nextRun, err := f.database.SpaceRun(ctx, f.alice.ID, next.ChildRunID)
	if err != nil || nextRun.ModelUserID != f.alice.ID || nextRun.ParentRunID != request.ChildRunID || nextRun.DelegationDepth != 2 {
		t.Fatalf("agent-to-agent work = %#v, %v", nextRun, err)
	}
	if _, err := f.database.CreateAgentMemberRequest(ctx, AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.alice.ID,
		RequesterAgentID: f.aliceAgent.ID, RequesterRunID: ReplyRequester(reply.ID), TargetAgentID: f.bobAgent.ID,
		Message: "@Researcher", IdempotencyKey: "spoof"}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a reply spoken for by another agent = %v", err)
	}
	second, err := f.database.CreateAgentMemberRequestMessage(ctx, next, "agent_member_reply", "Added. @Researcher anything else?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.CreateAgentMemberRequest(ctx, AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.alice.ID,
		RequesterAgentID: f.aliceAgent.ID, RequesterRunID: ReplyRequester(second.ID), TargetAgentID: f.bobAgent.ID,
		Message: "anything else?", IdempotencyKey: f.bobAgent.ID}); !errors.Is(err, ErrAgentRequestLimit) {
		t.Fatalf("a second hop = %v, want the depth limit", err)
	}

	plain, _, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, []MessageSpan{{Type: "text", Text: "No mention here"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ask.RequesterRunID = MentionRequester(plain.ID)
	if _, err := f.database.CreateAgentMemberRequest(ctx, ask); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a request naming a message without the mention = %v", err)
	}
}

func TestAgentArtifactsSaveToSpacesTheAgentIsIn(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	digest := strings.Repeat("a", 64)
	provenance := &AgentArtifactProvenance{AgentID: f.bobAgent.ID, ConversationID: "conversation_private", RunID: "run_private", Kind: "download"}
	elsewhere := createTestSpace(t, f.database, ctx, f.bob.ID, "Side project")
	if _, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.bob.ID, elsewhere.ID, "", "library",
		"report.pdf", "application/pdf", 32, digest, "library/elsewhere", "elsewhere-token", time.Now().Add(time.Hour), provenance); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("saving an artifact outside the agent's Space = %v", err)
	}
	if _, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.alice.ID, f.space.ID, "", "library",
		"report.pdf", "application/pdf", 32, digest, "library/notmine", "notmine-token", time.Now().Add(time.Hour), provenance); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("saving an artifact for someone else's agent = %v", err)
	}
	upload, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.bob.ID, f.space.ID, "", "library",
		"report.pdf", "application/pdf", 32, digest, "library/agentartifact", "artifact-token", time.Now().Add(time.Hour), provenance)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetLibraryUploadState(ctx, f.bob.ID, f.space.ID, upload.ID, "artifact-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	done, err := f.database.CompleteLibraryUpload(ctx, f.bob.ID, f.space.ID, upload.ID, "artifact-token", 32, digest, "application/pdf", nil)
	if err != nil || done.Item == nil {
		t.Fatalf("finished upload = %#v, %v", done, err)
	}

	mine, err := f.database.SpaceAgentArtifacts(ctx, f.bob.ID, f.space.ID, "", 0)
	if err != nil || len(mine) != 1 || mine[0].ItemID != done.Item.ID || mine[0].AgentName != "Researcher" || mine[0].Kind != "download" || mine[0].RunID != "run_private" {
		t.Fatalf("owner's view = %#v, %v", mine, err)
	}
	theirs, err := f.database.SpaceAgentArtifacts(ctx, f.alice.ID, f.space.ID, f.bobAgent.ID, 0)
	if err != nil || len(theirs) != 1 || theirs[0].OwnerName != f.bob.Username || theirs[0].ConversationID != "" || theirs[0].RunID != "" {
		t.Fatalf("another member's view = %#v, %v, want the item without Bob's private conversation", theirs, err)
	}
}
