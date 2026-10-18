package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMentionsAndTranscriptBelongToThePersonWhoAsked(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	mention := []MessageSpan{{Type: "text", Text: "Check this "}, {Type: "mention", AgentID: f.bobAgent.ID, Label: "Researcher"}}
	message, _, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, mention, nil)
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.database.CreateAgentMemberRequest(ctx, AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.alice.ID,
		RequesterRunID: MentionRequester(message.ID), TargetAgentID: f.bobAgent.ID, Message: "Check this @Researcher", IdempotencyKey: f.bobAgent.ID})
	if err != nil {
		t.Fatal(err)
	}
	// A request another agent made is not a mention.
	if _, err := f.request("agent-call"); err != nil {
		t.Fatal(err)
	}

	mine, err := f.database.AgentMentionsForUser(ctx, f.alice.ID, 0)
	if err != nil || len(mine) != 1 || mine[0].ID != request.ID {
		t.Fatalf("Alice's mentions = %#v, %v, want only her chat mention", mine, err)
	}
	if theirs, err := f.database.AgentMentionsForUser(ctx, f.bob.ID, 0); err != nil || len(theirs) != 0 {
		t.Fatalf("the agent owner's mentions = %#v, %v, want none", theirs, err)
	}

	steps, err := f.database.AgentRequestTranscript(ctx, f.alice.ID, request.ID)
	if err != nil || steps == nil {
		t.Fatalf("transcript for the asker = %#v, %v", steps, err)
	}
	if _, err := f.database.AgentRequestTranscript(ctx, f.bob.ID, request.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("transcript for the agent's owner = %v, want not found", err)
	}
}
