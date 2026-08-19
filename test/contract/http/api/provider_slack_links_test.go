package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type fakeSlackChatProvider struct{ posts atomic.Int32 }

func (f *fakeSlackChatProvider) Identity(context.Context) (SlackChatIdentity, error) {
	return SlackChatIdentity{TeamID: "T1", TeamName: "Misty", UserID: "U_BOT"}, nil
}
func (f *fakeSlackChatProvider) Channel(context.Context, string) (SlackChatChannel, error) {
	return SlackChatChannel{ID: "C1", Name: "general"}, nil
}
func (f *fakeSlackChatProvider) History(context.Context, string, string, string) (SlackChatPage, error) {
	return SlackChatPage{Messages: []SlackChatMessage{{Type: "message", UserID: "U_ALICE",
		Timestamp: "1710000000.000001", Text: "Root", ReplyCount: 1}}}, nil
}
func (f *fakeSlackChatProvider) Replies(context.Context, string, string) ([]SlackChatMessage, error) {
	return []SlackChatMessage{{Type: "message", UserID: "U_ALICE", Timestamp: "1710000000.000001", Text: "Root"},
		{Type: "message", UserID: "U_BOB", Timestamp: "1710000001.000001",
			ThreadTimestamp: "1710000000.000001", Text: "Reply"}}, nil
}
func (f *fakeSlackChatProvider) Post(context.Context, string, string, string, string) (string, error) {
	f.posts.Add(1)
	return "1710000002.000001", nil
}

func TestSlackChatLoopPreventionAndNormalization(t *testing.T) {
	link := &db.SpaceSlackLink{Direction: "two_way", BotUserID: "U_BOT"}
	if TestingShouldMirrorSlackMessage(SlackChatMessage{Type: "message", UserID: "U_BOT", Timestamp: "1.1", Text: "loop"}, link) {
		t.Fatal("Misty bot echo was accepted")
	}
	if TestingShouldMirrorSlackMessage(SlackChatMessage{Type: "message", BotID: "B_OTHER", Timestamp: "1.2", Text: "bot"}, link) {
		t.Fatal("Slack bot message was accepted")
	}
	message := SlackChatMessage{Type: "message", UserID: "U_PERSON", Timestamp: "1.3",
		Text: "hello", Files: []SlackChatFile{{Name: "brief.pdf", URL: "https://files.slack.test/brief"}}}
	if !TestingShouldMirrorSlackMessage(message, link) {
		t.Fatal("person-authored Slack message was rejected")
	}
	spans := TestingSlackContentToSpans(message)
	if len(spans) != 1 || spans[0].Text != "hello\n📎 brief.pdf" {
		t.Fatalf("Slack spans = %#v", spans)
	}
	link.Direction = "outbound"
	if TestingShouldMirrorSlackMessage(message, link) {
		t.Fatal("outbound-only link imported Slack traffic")
	}
}

func TestSlackLinkJSONUsesCanonicalSnakeCase(t *testing.T) {
	raw, err := json.Marshal(db.SpaceSlackLink{ID: "slacklink-1", SpaceID: "space-1",
		IntegrationID: "integration-1", SharedResourceID: "resource-1", ConversationID: "conversation-1",
		ConnectedByUserID: "user-1", TeamID: "T1", TeamName: "Misty", ChannelID: "C1",
		ChannelName: "general", Direction: "two_way", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	for _, key := range []string{"id", "space_id", "integration_id", "shared_resource_id", "conversation_id", "connected_by_user_id", "team_id", "team_name", "channel_id", "channel_name", "direction", "status"} {
		if _, exists := value[key]; !exists {
			t.Fatalf("Slack JSON missing %q: %s", key, raw)
		}
	}
	if _, exists := value["ChannelID"]; exists {
		t.Fatalf("Slack JSON exposed PascalCase: %s", raw)
	}
}

func TestSlackLinkHTTPCreateSyncAndIdempotentPublish(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Slack HTTP", uniqueTestEmail("slack-http"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "Slack HTTP")
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := spaces.TestingEncryptProviderAccessToken("slack", "slack-access-token")
	if err != nil {
		t.Fatal(err)
	}
	integration, err := database.SaveProviderCredential(t.Context(), db.ProviderCredential{SpaceID: space.ID,
		UserID: owner.ID, Provider: "slack", Ciphertext: ciphertext, Nonce: nonce, KeyVersion: 1,
		AccountID: "T1", AccountDisplay: "Misty"}, "Misty", []string{"channels:history", "chat:write"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSlackChatProvider{}
	spaces.TestingSetSlackChatProviderFactory(func(token, tokenType string) SlackChatProvider {
		if token != "slack-access-token" || tokenType != "Bearer" {
			t.Fatalf("provider credentials = %q/%q", token, tokenType)
		}
		return fake
	})
	router := chi.NewRouter()
	router.MethodFunc(http.MethodGet, "/spaces/{spaceID}/integrations/slack/links", spaces.SpaceSlackLinks())
	router.MethodFunc(http.MethodPost, "/spaces/{spaceID}/integrations/slack/links", spaces.SpaceSlackLinks())
	router.Post("/spaces/{spaceID}/integrations/slack/links/{linkID}/sync", spaces.SyncSpaceSlackLink())
	router.Post("/spaces/{spaceID}/integrations/slack/links/{linkID}/publish", spaces.PublishSpaceSlackMessage())
	token := newConversationTestBearerToken(t, database, owner.ID)
	path := "/spaces/" + space.ID + "/integrations/slack/links"
	created := performConversationRequest(t, router, http.MethodPost, path, token,
		map[string]any{"integration_id": integration.ID, "channel_id": "C1", "direction": "two_way"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		Link     db.SpaceSlackLink `json:"link"`
		Imported int               `json:"imported"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || response.Link.ID == "" || response.Imported != 2 {
		t.Fatalf("create response=%#v err=%v body=%s", response, err, created.Body.String())
	}
	listed := performConversationRequest(t, router, http.MethodGet, path, token, nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "ChannelID") {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	native, _, err := database.CreateSpaceConversationMessageWithReferences(t.Context(), owner.ID,
		space.ID, response.Link.ConversationID, []db.MessageSpan{{Type: "text", Text: "Publish once"}},
		nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	publishPath := path + "/" + response.Link.ID + "/publish"
	for attempt := 0; attempt < 2; attempt++ {
		published := performConversationRequest(t, router, http.MethodPost, publishPath, token,
			map[string]any{"message_id": native.ID, "thread_ts": "1710000000.000001"})
		if published.Code != http.StatusOK {
			t.Fatalf("publish %d status=%d body=%s", attempt, published.Code, published.Body.String())
		}
	}
	if fake.posts.Load() != 1 {
		t.Fatalf("Slack post calls=%d, want 1", fake.posts.Load())
	}
}
