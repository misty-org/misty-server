package db

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestDiscordChannelsCreateSharedReusableConversations(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Discord Owner", "discord-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Discord Member", "discord-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Discord")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	integration, err := database.SaveProviderCredential(ctx, ProviderCredential{
		SpaceID:        space.ID,
		UserID:         owner.ID,
		Provider:       "discord",
		Ciphertext:     []byte("encrypted"),
		Nonce:          []byte("nonce"),
		KeyVersion:     1,
		AccountID:      "discord-account",
		AccountDisplay: "Misty Discord",
	}, "Misty Discord", []string{})
	if err != nil {
		t.Fatal(err)
	}

	create := func(channelID, displayName string) *SpaceDiscordLink {
		t.Helper()
		link, createErr := database.CreateSpaceDiscordLink(ctx, owner.ID, SpaceDiscordLink{
			SpaceID:       space.ID,
			IntegrationID: integration.ID,
			GuildID:       "guild-1",
			GuildName:     "Misty Guild",
			ChannelID:     channelID,
			ChannelName:   displayName,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return link
	}

	first := create("channel-1", "Misty Guild / #general")
	second := create("channel-2", "Misty Guild / #design")
	if first.Direction != "two_way" || second.Direction != "two_way" {
		t.Fatalf("Discord defaults = %q/%q, want two_way", first.Direction, second.Direction)
	}
	if first.ConversationID == "" || first.ConversationID == second.ConversationID {
		t.Fatalf("conversation IDs = %q/%q", first.ConversationID, second.ConversationID)
	}
	links, err := database.SpaceDiscordLinksFor(ctx, member.ID, space.ID)
	if err != nil || len(links) != 2 {
		t.Fatalf("member links = %#v, %v", links, err)
	}
	conversations, err := database.SpaceConversations(ctx, member.ID, space.ID)
	if err != nil || len(conversations) != 2 {
		t.Fatalf("member conversations = %#v, %v", conversations, err)
	}
	for _, conversation := range conversations {
		if conversation.Origin != "discord" || !conversation.VisibleToSpace {
			t.Fatalf("Discord conversation = %#v", conversation)
		}
	}
	content := []MessageSpan{{Type: "text", Text: "from Discord"}}
	parent, err := database.CreateMirroredSpaceMessage(ctx, *first, content, MessageOrigin{
		System: "discord", ExternalID: "discord-parent", ExternalChannelID: first.ChannelID, AuthorName: "Rey",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedReplyID, err := database.DiscordExternalReplyID(ctx, space.ID, parent.ID, first.ChannelID)
	if err != nil || resolvedReplyID != "discord-parent" {
		t.Fatalf("resolved Discord reply = %q, %v", resolvedReplyID, err)
	}
	reply, err := database.CreateMirroredSpaceMessage(ctx, *first, content, MessageOrigin{
		System: "discord", ExternalID: "discord-reply", ExternalChannelID: first.ChannelID,
		ReplyToExternalID: "discord-parent", AuthorName: "Poe",
	})
	if err != nil || reply.ReplyToMessageID != parent.ID {
		t.Fatalf("mirrored reply = %#v, %v", reply, err)
	}
	if _, err := database.CreateMirroredSpaceMessage(ctx, *first, content, MessageOrigin{
		System: "discord", ExternalID: "discord-parent", ExternalChannelID: first.ChannelID,
	}); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("duplicate mirror error = %v, want ErrSpaceConflict", err)
	}

	// Gateway and manual sync can race with the same Discord snowflake. Exactly
	// one insert should win even when both transactions begin together.
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, createErr := database.CreateMirroredSpaceMessage(ctx, *first, content, MessageOrigin{
				System: "discord", ExternalID: "discord-race", ExternalChannelID: first.ChannelID,
			})
			results <- createErr
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		if result == nil {
			successes++
		} else if errors.Is(result, ErrSpaceConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent mirror error = %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent mirror outcomes = %d success, %d conflict", successes, conflicts)
	}
	native, _, err := database.CreateSpaceConversationMessageWithReferences(
		ctx, owner.ID, space.ID, first.ConversationID,
		[]MessageSpan{{Type: "text", Text: "publish once"}}, nil, nil, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimSpaceMessageDiscordPublish(ctx, owner.ID, space.ID, native.ID, first.ChannelID)
	if err != nil || !claimed {
		t.Fatalf("first publish claim = %t, %v", claimed, err)
	}
	claimed, err = database.ClaimSpaceMessageDiscordPublish(ctx, owner.ID, space.ID, native.ID, first.ChannelID)
	if err != nil || claimed {
		t.Fatalf("duplicate publish claim = %t, %v", claimed, err)
	}
	claimed, err = database.ClaimSpaceMessageDiscordPublish(ctx, member.ID, space.ID, native.ID, first.ChannelID)
	if err != nil || claimed {
		t.Fatalf("other member publish claim = %t, %v", claimed, err)
	}
	if _, err := database.SetSpaceMessageOrigin(ctx, space.ID, native.ID, MessageOrigin{
		System: "misty", PublishState: "failed", ExternalChannelID: first.ChannelID,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err = database.ClaimSpaceMessageDiscordPublish(ctx, owner.ID, space.ID, native.ID, first.ChannelID)
	if err != nil || !claimed {
		t.Fatalf("failed publish retry claim = %t, %v", claimed, err)
	}
	if _, err := database.CreateSpaceDiscordLink(ctx, member.ID, SpaceDiscordLink{
		SpaceID: space.ID, IntegrationID: integration.ID, GuildID: "guild-1", ChannelID: "channel-3",
	}); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("member link creation error = %v, want ErrLibraryForbidden", err)
	}

	renamed := create("channel-1", "Misty Guild / #announcements")
	if renamed.ID != first.ID || renamed.ConversationID != first.ConversationID {
		t.Fatalf("duplicate selection created a new identity: first=%#v renamed=%#v", first, renamed)
	}
	if err := database.DeleteSpaceDiscordLink(ctx, owner.ID, space.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.Conn.QueryRowContext(
		ctx,
		`SELECT integration_status FROM space_conversations WHERE id=$1`,
		first.ConversationID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "disconnected" {
		t.Fatalf("unlinked conversation status = %q", status)
	}

	reconnected := create("channel-1", "Misty Guild / #announcements")
	if reconnected.ID != first.ID || reconnected.ConversationID != first.ConversationID {
		t.Fatalf("reconnect did not reuse history: first=%#v reconnected=%#v", first, reconnected)
	}
}
