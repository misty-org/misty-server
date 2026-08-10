package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) ProviderSharedResource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DisableProviderSharedResource(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "resourceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) discoverProviderResources(ctx context.Context, userID, spaceID, integrationID string) ([]availableProviderResource, error) {
	credential, err := s.database.ProviderCredential(ctx, userID, spaceID, integrationID)
	if err != nil {
		return nil, err
	}
	token, tokenType, err := s.providerAccessToken(ctx, userID, spaceID, integrationID)
	if err != nil {
		return nil, err
	}
	switch credential.Provider {
	case "slack":
		return discoverSlackChannels(ctx, token)
	case "discord":
		return discoverDiscordChannels(ctx)
	case "notion":
		return discoverNotionResources(ctx, token, tokenType)
	default:
		return nil, db.ErrSpaceInvalid
	}
}

func discoverSlackChannels(ctx context.Context, token string) ([]availableProviderResource, error) {
	items := []availableProviderResource{}
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		query := url.Values{"exclude_archived": {"true"}, "limit": {"200"}, "types": {"public_channel,private_channel"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		payload, err := providerJSONRequest(ctx, token, "Bearer", http.MethodGet, "https://slack.com/api/conversations.list?"+query.Encode(), nil, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			OK       bool   `json:"ok"`
			Error    string `json:"error"`
			Channels []struct {
				ID, Name            string
				IsPrivate, IsMember bool
			} `json:"channels"`
			Metadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		if json.Unmarshal(payload, &page) != nil || !page.OK {
			if page.Error == "" {
				page.Error = "invalid_response"
			}
			return nil, errors.New("slack resource discovery failed: " + page.Error)
		}
		for _, channel := range page.Channels {
			if channel.ID == "" || !channel.IsMember {
				continue
			}
			config, _ := json.Marshal(map[string]any{"private": channel.IsPrivate})
			items = append(items, availableProviderResource{Provider: "slack", ResourceType: "channel", ExternalResourceID: channel.ID, DisplayName: "#" + channel.Name, Configuration: config})
		}
		cursor = page.Metadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return items, nil
}

func discoverDiscordChannels(ctx context.Context) ([]availableProviderResource, error) {
	token := strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN"))
	if token == "" {
		return nil, errors.New("discord bot is not configured")
	}
	guildPayload, err := providerJSONRequest(ctx, token, "Bot", http.MethodGet, "https://discord.com/api/v10/users/@me/guilds?limit=200", nil, nil)
	if err != nil {
		return nil, err
	}
	var guilds []struct{ ID, Name string }
	if json.Unmarshal(guildPayload, &guilds) != nil {
		return nil, errors.New("discord guild response was invalid")
	}
	items := []availableProviderResource{}
	for _, guild := range guilds {
		payload, requestErr := providerJSONRequest(ctx, token, "Bot", http.MethodGet, "https://discord.com/api/v10/guilds/"+url.PathEscape(guild.ID)+"/channels", nil, nil)
		if requestErr != nil {
			continue
		}
		var channels []struct {
			ID, Name string
			Type     int
		}
		if json.Unmarshal(payload, &channels) != nil {
			continue
		}
		for _, channel := range channels {
			if channel.Type != 0 && channel.Type != 5 && channel.Type != 15 {
				continue
			}
			config, _ := json.Marshal(map[string]string{"guildId": guild.ID, "guildName": guild.Name})
			items = append(items, availableProviderResource{Provider: "discord", ResourceType: "channel", ExternalResourceID: channel.ID, DisplayName: guild.Name + " / #" + channel.Name, Configuration: config})
		}
	}
	return items, nil
}

func discoverNotionResources(ctx context.Context, token, tokenType string) ([]availableProviderResource, error) {
	payload, err := providerJSONRequest(ctx, token, tokenType, http.MethodPost, "https://api.notion.com/v1/search", map[string]any{"page_size": 100, "sort": map[string]string{"direction": "descending", "timestamp": "last_edited_time"}}, map[string]string{"Notion-Version": "2026-03-11"})
	if err != nil {
		return nil, err
	}
	var response struct {
		Results []map[string]any `json:"results"`
	}
	if json.Unmarshal(payload, &response) != nil {
		return nil, errors.New("notion search response was invalid")
	}
	items := []availableProviderResource{}
	for _, result := range response.Results {
		id, _ := result["id"].(string)
		object, _ := result["object"].(string)
		if id == "" {
			continue
		}
		resourceType := object
		if object == "data_source" || object == "database" || object == "page" {
		} else {
			continue
		}
		title := notionObjectTitle(result)
		if title == "" {
			title = "Untitled " + strings.ReplaceAll(resourceType, "_", " ")
		}
		items = append(items, availableProviderResource{Provider: "notion", ResourceType: resourceType, ExternalResourceID: id, DisplayName: title, Configuration: json.RawMessage(`{}`)})
	}
	return items, nil
}

func notionObjectTitle(value map[string]any) string {
	if title, ok := value["title"].([]any); ok {
		return notionRichText(title)
	}
	properties, _ := value["properties"].(map[string]any)
	for _, property := range properties {
		item, _ := property.(map[string]any)
		kind, _ := item["type"].(string)
		if kind != "title" {
			continue
		}
		values, _ := item["title"].([]any)
		if title := notionRichText(values); title != "" {
			return title
		}
	}
	return ""
}

func notionRichText(values []any) string {
	var text strings.Builder
	for _, raw := range values {
		value, _ := raw.(map[string]any)
		plain, _ := value["plain_text"].(string)
		text.WriteString(plain)
	}
	return strings.TrimSpace(text.String())
}

func providerJSONRequest(ctx context.Context, token, tokenType, method, endpoint string, body any, headers map[string]string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if tokenType == "" {
		tokenType = "Bearer"
	}
	request.Header.Set("Authorization", tokenType+" "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > 4<<20 {
		return nil, errors.New("provider response exceeded 4 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		digest := sha256.Sum256(payload)
		return payload, errors.New("provider request failed with " + response.Status + " (body " + hex.EncodeToString(digest[:6]) + ")")
	}
	return payload, nil
}
