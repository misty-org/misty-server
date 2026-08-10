package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

// PublishSpaceDiscordMessage mirrors one Misty message outward. The desktop
// calls it automatically for two-way Discord conversations and may retry a
// failed individual message without duplicating inbound traffic.
func (s *SpacesService) PublishSpaceDiscordMessage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, linkID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "linkID")
		var body struct {
			MessageID string `json:"message_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		links, err := s.database.SpaceDiscordLinksFor(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var link *db.SpaceDiscordLink
		for index := range links {
			if links[index].ID == linkID {
				link = &links[index]
				break
			}
		}
		if link == nil {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		if link.Direction == "inbound" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "outbound_disabled"})
			return
		}
		message, err := s.database.SpaceMessageForPublish(r.Context(), userID, spaceID, body.MessageID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if message.ConversationID != link.ConversationID {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		if !TestingPublishableToDiscord(message, userID) {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		origin := db.MessageOrigin{System: "misty", PublishState: "published", ExternalChannelID: link.ChannelID}
		external, publishErr := s.postDiscordMessage(r.Context(), link, message)
		if publishErr != nil {
			origin.PublishState = "failed"
			origin.PublishError = discordFailureMessage(providerErrorCode(publishErr))
		} else {
			origin.PublishedExternal = external
			origin.PublishedAt = time.Now().UTC().Format(time.RFC3339)
		}
		updated, err := s.database.SetSpaceMessageOrigin(r.Context(), spaceID, message.ID, origin)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": updated})
	}
}

// publishableToDiscord mirrors the client's rule set exactly. Agent output and
// system notices stay inside the Space, and a Discord-sourced message is never
// bounced back — that is what keeps the mirror from looping.
func TestingPublishableToDiscord(message *db.SpaceMessage, userID string) bool {
	if message.SenderKind != "person" || message.SenderUserID != userID {
		return false
	}
	if len(message.Origin) > 0 {
		var origin db.MessageOrigin
		if json.Unmarshal(message.Origin, &origin) == nil && origin.System != "" && origin.System != "misty" {
			return false
		}
	}
	return strings.TrimSpace(TestingSpansToPlainText(message.Content)) != ""
}

func TestingSpansToPlainText(spans []db.MessageSpan) string {
	var builder strings.Builder
	for _, span := range spans {
		if span.Type == "text" {
			builder.WriteString(span.Text)
			continue
		}
		builder.WriteString("@" + span.Label)
	}
	return builder.String()
}

// postDiscordMessage sends through the link's webhook when one exists, so the
// message carries its Misty author's name, and falls back to the bot identity
// with an attributed prefix otherwise.
func (s *SpacesService) postDiscordMessage(ctx context.Context, link *db.SpaceDiscordLink, message *db.SpaceMessage) (string, error) {
	content := TestingTruncateForDiscord(strings.TrimSpace(TestingSpansToPlainText(message.Content)))
	if content == "" {
		return "", db.ErrSpaceInvalid
	}
	author := strings.TrimSpace(message.SenderName)
	if author == "" {
		author = "Misty"
	}
	// An empty parse list means a mirrored "@everyone" cannot ping a Discord
	// server. Mirroring must never escalate reach on a user's behalf.
	payload := map[string]any{"allowed_mentions": map[string]any{"parse": []string{}}}

	if link.WebhookID != "" && len(link.WebhookCiphertext) > 0 {
		token, err := s.decryptProviderSecret("discord", link.WebhookCiphertext, link.WebhookNonce)
		if err == nil {
			payload["content"], payload["username"] = content, author
			endpoint := "https://discord.com/api/v10/webhooks/" + url.PathEscape(link.WebhookID) + "/" + url.PathEscape(string(token)) + "?wait=true"
			raw, postErr := providerJSONRequest(ctx, "", "", http.MethodPost, endpoint, payload, nil)
			if postErr == nil {
				var created struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(raw, &created)
				return created.ID, nil
			}
			// A deleted or rotated webhook must not block publishing; fall
			// through to the bot identity rather than failing the write.
		}
	}

	payload["content"] = TestingTruncateForDiscord("**" + author + "**: " + content)
	delete(payload, "username")
	endpoint := "https://discord.com/api/v10/channels/" + url.PathEscape(link.ChannelID) + "/messages"
	raw, err := providerJSONRequest(ctx, discordBotToken(), "Bot", http.MethodPost, endpoint, payload, nil)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &created)
	return created.ID, nil
}

func TestingTruncateForDiscord(content string) string {
	runes := []rune(content)
	if len(runes) <= TestingDiscordContentLimit {
		return content
	}
	return string(runes[:TestingDiscordContentLimit-1]) + "…"
}

// syncDiscordLink imports every message after the stored cursor.
func (s *SpacesService) syncDiscordLink(ctx context.Context, link *db.SpaceDiscordLink) (int, error) {
	if link == nil {
		return 0, nil
	}
	if discordBotToken() == "" {
		return 0, errors.New("discord bot is not configured")
	}
	if channel, err := s.discordChannel(ctx, link.ChannelID); err == nil &&
		strings.TrimSpace(channel.Name) != "" && channel.Name != link.ChannelName {
		if err := s.database.UpdateSpaceDiscordLinkDisplay(ctx, link.ID, channel.Name); err == nil {
			link.ChannelName = channel.Name
		}
	}
	if link.Direction == "outbound" {
		return 0, nil
	}
	values := url.Values{"limit": {"100"}}
	if link.LastMessageID != "" {
		values.Set("after", link.LastMessageID)
	}
	endpoint := "https://discord.com/api/v10/channels/" + url.PathEscape(link.ChannelID) + "/messages?" + values.Encode()
	raw, err := providerJSONRequest(ctx, discordBotToken(), "Bot", http.MethodGet, endpoint, nil, nil)
	if err != nil {
		return 0, err
	}
	var messages []TestingDiscordMessage
	if json.Unmarshal(raw, &messages) != nil {
		return 0, errors.New("discord message history was invalid")
	}
	// Discord returns newest first; mirror oldest first so the Space transcript
	// reads in the order the conversation actually happened.
	imported, cursor := 0, link.LastMessageID
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if TestingSnowflakeAfter(message.ID, cursor) {
			cursor = message.ID
		}
		if !TestingShouldMirrorDiscordMessage(message, link) {
			continue
		}
		if _, mirrorErr := s.mirrorDiscordMessage(ctx, *link, message); mirrorErr == nil {
			imported++
		}
	}
	now := time.Now().UTC()
	return imported, s.database.SetSpaceDiscordLinkSync(ctx, link.ID, cursor, "active", "", &now)
}

// shouldMirrorDiscordMessage decides whether a Discord message becomes a Misty
// message. The bot-and-webhook rule is what stops an infinite mirror loop:
// anything Misty itself posted comes back down the same channel read.
func TestingShouldMirrorDiscordMessage(message TestingDiscordMessage, link *db.SpaceDiscordLink) bool {
	if link.Direction == "outbound" {
		return false
	}
	if !mirroredDiscordMessageTypes[message.Type] {
		return false
	}
	if message.WebhookID != "" {
		return false
	}
	if link.BotUserID != "" && message.Author.ID == link.BotUserID {
		return false
	}
	if message.Author.Bot && link.BotUserID == "" {
		return false
	}
	return strings.TrimSpace(message.Content) != "" || len(message.Attachments) > 0
}
