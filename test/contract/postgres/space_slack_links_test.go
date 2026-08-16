package db

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSlackChannelCreatesDurableProviderConversation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Slack Owner", "slack-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Slack")
	integration, err := database.SaveProviderCredential(ctx, ProviderCredential{SpaceID: space.ID,
		UserID: owner.ID, Provider: "slack", Ciphertext: []byte("encrypted"), Nonce: []byte("nonce"),
		KeyVersion: 1, AccountID: "T123", AccountDisplay: "Misty workspace"},
		"Misty workspace", []string{"channels:history", "chat:write"})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := database.PublishProviderSharedResource(ctx, owner.ID, ProviderSharedResource{
		SpaceID: space.ID, IntegrationID: integration.ID, Provider: "slack", ResourceType: "channel",
		ExternalResourceID: "C123", DisplayName: "#general", Configuration: json.RawMessage(`{"accountId":"T123"}`)})
	if err != nil {
		t.Fatal(err)
	}
	link, err := database.CreateSpaceSlackLink(ctx, owner.ID, SpaceSlackLink{SpaceID: space.ID,
		IntegrationID: integration.ID, SharedResourceID: resource.ID, ChannelID: "C123", BotUserID: "U_BOT"})
	if err != nil {
		t.Fatal(err)
	}
	if link.ConversationID == "" || link.Direction != "two_way" || link.TeamID != "T123" {
		t.Fatalf("link = %#v", link)
	}
	encoded, _ := json.Marshal(link)
	var contract map[string]any
	_ = json.Unmarshal(encoded, &contract)
	for _, field := range []string{"id", "space_id", "integration_id", "shared_resource_id", "conversation_id", "channel_id", "last_message_ts"} {
		if _, ok := contract[field]; !ok && field != "last_message_ts" {
			t.Fatalf("Slack link contract missing %q: %s", field, encoded)
		}
	}
	if _, leaked := contract["ChannelID"]; leaked {
		t.Fatalf("Slack link leaked PascalCase JSON: %s", encoded)
	}

	rootOrigin := MessageOrigin{System: "slack", ExternalID: "1710000000.000001",
		ExternalChannelID: "C123", ExternalThreadID: "1710000000.000001", AuthorName: "U_ALICE"}
	root, created, err := database.UpsertProviderMirroredMessage(ctx, ProviderMirroredMessage{
		SpaceID: space.ID, ConversationID: link.ConversationID, ConnectedByUserID: owner.ID,
		Provider: "slack", Content: []MessageSpan{{Type: "text", Text: "Root"}}, Origin: rootOrigin})
	if err != nil || !created {
		t.Fatalf("root = %#v created=%v err=%v", root, created, err)
	}
	reply, created, err := database.UpsertProviderMirroredMessage(ctx, ProviderMirroredMessage{
		SpaceID: space.ID, ConversationID: link.ConversationID, ConnectedByUserID: owner.ID,
		Provider: "slack", Content: []MessageSpan{{Type: "text", Text: "Reply"}}, Origin: MessageOrigin{
			System: "slack", ExternalID: "1710000001.000001", ExternalChannelID: "C123",
			ExternalThreadID: rootOrigin.ExternalID, AuthorName: "U_BOB"}})
	if err != nil || !created || reply.ReplyToMessageID != root.ID {
		t.Fatalf("reply = %#v created=%v err=%v", reply, created, err)
	}
	edited, created, err := database.UpsertProviderMirroredMessage(ctx, ProviderMirroredMessage{
		SpaceID: space.ID, ConversationID: link.ConversationID, ConnectedByUserID: owner.ID,
		Provider: "slack", Content: []MessageSpan{{Type: "text", Text: "Root edited"}}, Origin: rootOrigin})
	if err != nil || created || edited.ID != root.ID || edited.Content[0].Text != "Root edited" {
		t.Fatalf("edited = %#v created=%v err=%v", edited, created, err)
	}
	conversations, err := database.SpaceConversations(ctx, owner.ID, space.ID)
	if err != nil || len(conversations) != 1 || conversations[0].Origin != "slack" || !conversations[0].VisibleToSpace {
		t.Fatalf("conversations = %#v err=%v", conversations, err)
	}
}
