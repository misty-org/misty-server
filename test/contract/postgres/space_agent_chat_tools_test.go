package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)


import ()

func TestPrivateSpaceAgentSendsExactMessageThroughServerOwnedToolbox(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("space-message-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Message Space")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	result, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, personal.ID,
		"Tell everyone Stone is off for today", "message.send",
		json.RawMessage(`{"message":"Stone is off for today","audience":"space"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		MessageID string `json:"message_id"`
	}
	if json.Unmarshal(result, &sent) != nil || sent.MessageID == "" {
		t.Fatalf("send result = %s", result)
	}
	messages, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("shared Agent messages = %#v, %v", messages, err)
	}
	rawContent, _ := json.Marshal(messages[0].Content)
	if messages[0].SenderAgentID != personal.ID ||
		!strings.Contains(string(rawContent), "Stone is off for today") {
		t.Fatalf("shared Agent messages = %#v, %v", messages, err)
	}
}

func TestMistyRoutesOnePersonPrivatelyAndPreservesStructuredMention(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("private-message-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("private-message-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Family")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}

	result, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "",
		"Tell Melissa Chen to please finish the laundry by tonight", "message.send",
		json.RawMessage(`{"message":"@`+member.Username+`, please finish the laundry by tonight.","audience":"auto","recipientUserId":"`+member.ID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		MessageID      string `json:"message_id"`
		ConversationID string `json:"conversation_id"`
		Audience       string `json:"audience"`
	}
	if err := json.Unmarshal(result, &sent); err != nil || sent.MessageID == "" || sent.ConversationID == "" || sent.Audience != "private" {
		t.Fatalf("private send result = %s, %v", result, err)
	}
	shared, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 10)
	if err != nil || len(shared) != 0 {
		t.Fatalf("private message leaked into shared chat: %#v, %v", shared, err)
	}
	messages, err := database.SpaceConversationMessages(ctx, member.ID, space.ID, sent.ConversationID, 0, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("private conversation messages = %#v, %v", messages, err)
	}
	var origin struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(messages[0].Origin, &origin) != nil || messages[0].SenderKind != "system" || origin.Kind != "misty_assistant" {
		t.Fatalf("Misty identity provenance = %#v", messages[0])
	}
	if len(messages[0].Content) < 2 || messages[0].Content[0].Type != "mention" || messages[0].Content[0].UserID != member.ID || messages[0].Content[0].Label != member.Username {
		t.Fatalf("structured mention content = %#v", messages[0].Content)
	}

	groupResult, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "",
		"Post in the group chat that Melissa Chen will finish the laundry tonight", "message.send",
		json.RawMessage(`{"message":"@`+member.Username+` will finish the laundry tonight.","audience":"space","recipientUserId":"`+member.ID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var groupSent struct {
		Audience string `json:"audience"`
	}
	if json.Unmarshal(groupResult, &groupSent) != nil || groupSent.Audience != "space" {
		t.Fatalf("group send result = %s", groupResult)
	}
	shared, err = database.SpaceMessages(ctx, member.ID, space.ID, 0, 10)
	if err != nil || len(shared) != 1 || shared[0].Content[0].Type != "mention" || shared[0].Content[0].UserID != member.ID {
		t.Fatalf("shared mention message = %#v, %v", shared, err)
	}

	_, err = api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "",
		"Send the laundry update", "message.send",
		json.RawMessage(`{"message":"Laundry update","audience":"auto"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "choose audience") {
		t.Fatalf("ambiguous audience error = %v", err)
	}
	shared, err = database.SpaceMessages(ctx, member.ID, space.ID, 0, 10)
	if err != nil || len(shared) != 1 {
		t.Fatalf("ambiguous message should not be sent: %#v, %v", shared, err)
	}
}

func TestFamilySpaceResearchCanBeSavedAndPostedWithCitations(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("family-research-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Family Space")
	if err != nil {
		t.Fatal(err)
	}
	prompt := "Research summer camps in Pasadena, save the research, and post a cited summary to Family Space"
	sourceURL := "https://example.org/pasadena-camps"
	createdRaw, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "", prompt, "note.create",
		json.RawMessage(`{"title":"Pasadena summer camp research","markdown":"# Summer camps\n\nArt and science programs are available.\n\nSource: `+sourceURL+`"}`),
	)
	if err != nil || !strings.Contains(string(createdRaw), "Pasadena summer camp research") {
		t.Fatalf("saved research note = %s, %v", createdRaw, err)
	}
	summary := "Pasadena summer camps include art and science programs. Source: " + sourceURL
	if _, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "", prompt, "message.send",
		json.RawMessage(`{"message":"`+summary+`","audience":"space"}`),
	); err != nil {
		t.Fatalf("post cited summary: %v", err)
	}
	messages, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 20)
	if err != nil || len(messages) != 1 || !strings.Contains(api.TestingSpansToPlainText(messages[0].Content), sourceURL) {
		t.Fatalf("Family Space cited summary = %#v, %v", messages, err)
	}
}
