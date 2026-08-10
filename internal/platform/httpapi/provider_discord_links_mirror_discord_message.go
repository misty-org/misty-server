package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// mirrorDiscordMessage writes one Discord message into the linked Space.
func (s *SpacesService) mirrorDiscordMessage(ctx context.Context, link db.SpaceDiscordLink, message TestingDiscordMessage) (*db.SpaceMessage, error) {
	labels := map[string]string{}
	for _, mention := range message.Mentions {
		name := strings.TrimSpace(mention.GlobalName)
		if name == "" {
			name = mention.Username
		}
		labels[mention.ID] = name
	}
	content := TestingDiscordContentToSpans(message, labels)
	origin := db.MessageOrigin{
		System: "discord", ExternalID: message.ID, ExternalChannelID: message.ChannelID,
		AuthorName: TestingDiscordDisplayName(message), AuthorHandle: message.Author.Username,
		AuthoredAt: message.Timestamp,
	}
	if message.Author.Avatar != "" {
		origin.AuthorAvatarURL = "https://cdn.discordapp.com/avatars/" + message.Author.ID + "/" + message.Author.Avatar + ".png"
	}
	for _, attachment := range message.Attachments {
		origin.AttachmentURLs = append(origin.AttachmentURLs, attachment.URL)
	}
	return s.database.CreateMirroredSpaceMessage(ctx, link, content, origin)
}

// discordContentToSpans rewrites Discord's id-based mention tokens into text a
// person can read, rather than leaking `<@980…>` into the transcript.
func TestingDiscordContentToSpans(message TestingDiscordMessage, labels map[string]string) []db.MessageSpan {
	text := message.Content
	replaceMention := func(pattern *regexp.Regexp, prefix string) {
		text = pattern.ReplaceAllStringFunc(text, func(token string) string {
			matches := pattern.FindStringSubmatch(token)
			if len(matches) < 2 {
				return token
			}
			if label, exists := labels[matches[1]]; exists && label != "" {
				return prefix + label
			}
			return token
		})
	}
	replaceMention(discordUserMentionPattern, "@")
	replaceMention(discordRoleMentionPattern, "@")
	replaceMention(discordChannelMentionPattern, "#")

	if len(message.Attachments) > 0 {
		names := make([]string, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			names = append(names, attachment.Filename)
		}
		summary := "📎 " + strings.Join(names, ", ")
		if strings.TrimSpace(text) == "" {
			text = summary
		} else {
			text += "\n" + summary
		}
	}
	// Misty caps a message at 4000 characters. A Discord message plus its
	// attachment summary can exceed that, and a rejected insert would drop the
	// message entirely — trimming keeps the transcript complete.
	if runes := []rune(text); len(runes) > TestingMistyMessageCharLimit {
		text = string(runes[:TestingMistyMessageCharLimit-1]) + "…"
	}
	return []db.MessageSpan{{Type: "text", Text: text}}
}

// Mirrors db.MaxMessageChars, which mirrored content must also respect.
const TestingMistyMessageCharLimit = 4000

func TestingDiscordDisplayName(message TestingDiscordMessage) string {
	if name := strings.TrimSpace(message.Author.GlobalName); name != "" {
		return name
	}
	if name := strings.TrimSpace(message.Author.Username); name != "" {
		return name
	}
	return "Discord user"
}

// snowflakeAfter compares Discord ids numerically. A plain string comparison
// would order "9" after "10" and silently rewind the cursor.
func TestingSnowflakeAfter(candidate, reference string) bool {
	if reference == "" {
		return true
	}
	if len(candidate) != len(reference) {
		return len(candidate) > len(reference)
	}
	return candidate > reference
}

type discordChannelMetadata struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	GuildID string `json:"guild_id"`
	Type    int    `json:"type"`
}

func (s *SpacesService) discordChannel(ctx context.Context, channelID string) (discordChannelMetadata, error) {
	var channel discordChannelMetadata
	if strings.TrimSpace(channelID) == "" {
		return channel, db.ErrSpaceInvalid
	}
	raw, err := providerJSONRequest(ctx, discordBotToken(), "Bot", http.MethodGet,
		"https://discord.com/api/v10/channels/"+url.PathEscape(channelID), nil, nil)
	if err != nil {
		return channel, err
	}
	if json.Unmarshal(raw, &channel) != nil || channel.ID == "" {
		return channel, errors.New("discord channel response was invalid")
	}
	return channel, nil
}

func (s *SpacesService) discordBotIdentity(ctx context.Context) (string, error) {
	raw, err := providerJSONRequest(ctx, discordBotToken(), "Bot", http.MethodGet,
		"https://discord.com/api/v10/users/@me", nil, nil)
	if err != nil {
		return "", err
	}
	var identity struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &identity) != nil || identity.ID == "" {
		return "", errors.New("discord identity response was invalid")
	}
	return identity.ID, nil
}

func (s *SpacesService) createDiscordWebhook(ctx context.Context, channelID string) (string, string, error) {
	raw, err := providerJSONRequest(ctx, discordBotToken(), "Bot", http.MethodPost,
		"https://discord.com/api/v10/channels/"+url.PathEscape(channelID)+"/webhooks",
		map[string]any{"name": "Misty"}, nil)
	if err != nil {
		return "", "", err
	}
	var webhook struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &webhook) != nil || webhook.ID == "" || webhook.Token == "" {
		return "", "", errors.New("discord webhook response was invalid")
	}
	return webhook.ID, webhook.Token, nil
}

// discordFailureMessage turns a provider error code into user-facing copy.
// Discord's own error bodies are not something a person should have to read.
func discordFailureMessage(code string) string {
	switch code {
	case "permission_missing":
		return "Misty lost access to this Discord channel. Check the bot's permissions."
	case "connection_revoked":
		return "Reconnect Discord to keep mirroring this channel."
	case "rate_limited":
		return "Discord is rate limiting Misty. Syncing will resume shortly."
	case "not_found":
		return "That Discord channel no longer exists."
	default:
		return "Misty could not reach Discord just now."
	}
}
