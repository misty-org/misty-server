package social

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DiscordAdapter struct {
	Client   *http.Client
	APIBase  string
	BotToken string
}

func (DiscordAdapter) Provider() SocialProviderID { return SocialProviderDiscord }
func (DiscordAdapter) Capabilities() SocialCapabilitySet {
	return SocialCapabilitySet{Read: true, Send: true, Schedule: true, Automate: true}
}

func (a DiscordAdapter) DiscoverResources(ctx context.Context, token string) ([]SocialResource, error) {
	guilds, err := a.discordGet(ctx, token, "/users/@me/guilds")
	if err != nil {
		return nil, err
	}
	var values []struct{ ID, Name string }
	if err := json.Unmarshal(guilds, &values); err != nil {
		return nil, err
	}
	resources := make([]SocialResource, 0)
	for _, guild := range values {
		channelToken := token
		channelScheme := "Bearer"
		if strings.TrimSpace(a.BotToken) != "" {
			channelToken = a.BotToken
			channelScheme = "Bot"
		}
		channels, channelErr := a.discordGetWithScheme(
			ctx,
			channelScheme,
			channelToken,
			"/guilds/"+url.PathEscape(guild.ID)+"/channels",
		)
		if channelErr != nil {
			continue
		}
		var items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type int    `json:"type"`
		}
		if json.Unmarshal(channels, &items) != nil {
			continue
		}
		for _, item := range items {
			if item.Type == 0 || item.Type == 5 || item.Type == 15 {
				resources = append(resources, SocialResource{ID: item.ID, ParentID: guild.ID, Name: item.Name, Kind: "channel"})
			}
		}
	}
	return resources, nil
}

func (a DiscordAdapter) NormalizeEvent(_ context.Context, raw []byte) ([]SocialMessage, error) {
	var event struct {
		Type string `json:"t"`
		Data struct {
			ID                  string `json:"id"`
			ChannelID           string `json:"channel_id"`
			Content             string `json:"content"`
			Timestamp           string `json:"timestamp"`
			ReferencedMessageID string `json:"referenced_message_id"`
			Author              struct {
				ID         string `json:"id"`
				Username   string `json:"username"`
				GlobalName string `json:"global_name"`
				Avatar     string `json:"avatar"`
				Bot        bool   `json:"bot"`
			} `json:"author"`
		} `json:"d"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	if event.Type != "MESSAGE_CREATE" || event.Data.ID == "" || event.Data.ChannelID == "" {
		return nil, nil
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, event.Data.Timestamp)
	name := event.Data.Author.GlobalName
	if name == "" {
		name = event.Data.Author.Username
	}
	kind := "person"
	if event.Data.Author.Bot {
		kind = "bot"
	}
	return []SocialMessage{{
		ExternalID: event.Data.ID, ConversationID: event.Data.ChannelID,
		Provider: SocialProviderDiscord, Direction: SocialMessageInbound,
		DeliveryState: SocialDeliveryDelivered, Text: event.Data.Content, CreatedAt: createdAt,
		Identity: SocialIdentity{Provider: SocialProviderDiscord, ExternalUserID: event.Data.Author.ID, DisplayName: name, Handle: event.Data.Author.Username, Kind: kind},
		Raw:      append(json.RawMessage(nil), raw...),
	}}, nil
}

func (a DiscordAdapter) Send(ctx context.Context, botToken string, command SocialOutboundCommand) (SocialSendReceipt, error) {
	text := PlainText(command.Content)
	if strings.TrimSpace(botToken) == "" || text == "" || command.ExternalResourceID == "" {
		return SocialSendReceipt{}, fmt.Errorf("discord send is missing a bot token, destination, or content")
	}
	payload, _ := json.Marshal(map[string]any{"content": text, "nonce": command.IdempotencyKey, "enforce_nonce": true})
	endpoint := a.base() + "/channels/" + url.PathEscape(command.ExternalResourceID) + "/messages"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return SocialSendReceipt{}, err
	}
	request.Header.Set("Authorization", "Bot "+botToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client().Do(request)
	if err != nil {
		return SocialSendReceipt{}, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SocialSendReceipt{}, fmt.Errorf("discord send returned %s", response.Status)
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &result) != nil || result.ID == "" {
		return SocialSendReceipt{}, fmt.Errorf("discord send returned an invalid receipt")
	}
	return SocialSendReceipt{ExternalID: result.ID, State: "sent", Raw: body}, nil
}

func (a DiscordAdapter) discordGet(ctx context.Context, token, path string) ([]byte, error) {
	return a.discordGetWithScheme(ctx, "Bearer", token, path)
}

func (a DiscordAdapter) discordGetWithScheme(
	ctx context.Context,
	scheme string,
	token string,
	path string,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", scheme+" "+token)
	response, err := a.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("discord returned %s", response.Status)
	}
	return body, nil
}
func (a DiscordAdapter) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}
func (a DiscordAdapter) base() string {
	if a.APIBase != "" {
		return strings.TrimRight(a.APIBase, "/")
	}
	return "https://discord.com/api/v10"
}
