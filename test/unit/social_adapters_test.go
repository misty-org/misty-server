package unit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	social "github.com/kannachi323/misty/server/internal/integrations/social"
)

func TestDiscordDiscoveryUsesUserIdentityAndBotChannelAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/users/@me/guilds":
			if request.Header.Get("Authorization") != "Bearer user-token" {
				t.Errorf("guild authorization = %q", request.Header.Get("Authorization"))
			}
			fmt.Fprint(writer, `[{"id":"g1","name":"Misty"}]`)
		case "/guilds/g1/channels":
			if request.Header.Get("Authorization") != "Bot bot-token" {
				t.Errorf("channel authorization = %q", request.Header.Get("Authorization"))
			}
			fmt.Fprint(writer, `[{"id":"c1","name":"general","type":0}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	resources, err := (social.DiscordAdapter{
		Client: server.Client(), APIBase: server.URL, BotToken: "bot-token",
	}).DiscoverResources(context.Background(), "user-token")
	if err != nil || len(resources) != 1 || resources[0].ID != "c1" {
		t.Fatalf("resources = %#v, err = %v", resources, err)
	}
}

func TestDiscordNormalizeMessage(t *testing.T) {
	raw := `{"t":"MESSAGE_CREATE","d":{"id":"m1","channel_id":"c1","content":"hello","timestamp":"2026-08-28T12:00:00Z","author":{"id":"u1","username":"matt","global_name":"Matthew","bot":false}}}`
	messages, err := (social.DiscordAdapter{}).NormalizeEvent(context.Background(), []byte(raw))
	if err != nil || len(messages) != 1 {
		t.Fatalf("normalize: %v %#v", err, messages)
	}
	if messages[0].Provider != social.SocialProviderDiscord || messages[0].Text != "hello" {
		t.Fatalf("unexpected message: %#v", messages[0])
	}
}

func TestInstagramIgnoresEchoes(t *testing.T) {
	raw := `{"entry":[{"messaging":[{"sender":{"id":"u1"},"recipient":{"id":"a1"},"timestamp":1000,"message":{"mid":"m1","text":"hello","is_echo":true}},{"sender":{"id":"u2"},"recipient":{"id":"a1"},"timestamp":2000,"message":{"mid":"m2","text":"hi"}}]}]}`
	messages, err := (social.InstagramAdapter{}).NormalizeEvent(context.Background(), []byte(raw))
	if err != nil || len(messages) != 1 || messages[0].ExternalID != "m2" {
		t.Fatalf("unexpected: %v %#v", err, messages)
	}
}

func TestPlainText(t *testing.T) {
	value := social.PlainText([]social.SocialContentSpan{{Type: "text", Text: "hello "}, {Type: "mention", Text: "ignored"}, {Type: "text", Text: "world"}})
	if strings.TrimSpace(value) != "hello world" {
		t.Fatalf("got %q", value)
	}
}
