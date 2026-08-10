package api

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) processDiscordDispatch(ctx context.Context, envelope discordGatewayEnvelope, raw []byte) error {
	switch envelope.Type {
	case "MESSAGE_CREATE", "MESSAGE_UPDATE", "MESSAGE_DELETE", "MESSAGE_REACTION_ADD", "MESSAGE_REACTION_REMOVE", "THREAD_CREATE", "THREAD_UPDATE", "THREAD_DELETE":
	default:
		return nil
	}
	var event struct {
		ID          string          `json:"id"`
		GuildID     string          `json:"guild_id"`
		ChannelID   string          `json:"channel_id"`
		Content     string          `json:"content"`
		Timestamp   string          `json:"timestamp"`
		Edited      string          `json:"edited_timestamp"`
		Attachments json.RawMessage `json:"attachments"`
		Mentions    json.RawMessage `json:"mentions"`
		Author      struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"author"`
		MessageID string `json:"message_id"`
		Emoji     any    `json:"emoji"`
	}
	if json.Unmarshal(envelope.Data, &event) != nil || event.GuildID == "" || event.ChannelID == "" {
		return errors.New("discord dispatch identity is missing")
	}
	// Mirror into any Space conversation linked to this channel. This is
	// independent of the shared-resource pipeline below: a Space can mirror a
	// channel without publishing it as a workflow resource, and vice versa.
	s.mirrorDiscordDispatch(ctx, envelope, event.GuildID, event.ChannelID)

	resources, err := s.database.MatchingProviderResources(ctx, "discord", event.GuildID, event.ChannelID)
	if err != nil {
		return err
	}
	externalID := event.ID
	if externalID == "" {
		externalID = event.MessageID
	}
	if externalID == "" {
		externalID = strconv.FormatInt(envelope.SequenceOrZero(), 10)
	}
	for _, resource := range resources {
		eventID := envelope.Type + ":" + strconv.FormatInt(envelope.SequenceOrZero(), 10) + ":" + externalID
		claimed, claimErr := s.database.EnqueueProviderEvent(ctx, resource, eventID, raw)
		if claimErr != nil || !claimed {
			continue
		}
		state := "processed"
		if event.Content == "" && len(event.Attachments) <= 2 && strings.HasPrefix(envelope.Type, "MESSAGE_") && envelope.Type != "MESSAGE_DELETE" && !event.Author.Bot {
			_ = s.database.SetProviderSharedResourceHealth(ctx, resource.ID, "needs_attention", "message_content_intent_missing")
			state = "failed"
		} else {
			_ = s.database.SetProviderSharedResourceHealth(ctx, resource.ID, "active", "")
			content := map[string]any{"event_type": envelope.Type, "id": externalID, "guild_id": event.GuildID, "channel_id": event.ChannelID, "text": event.Content, "author": event.Author, "attachments": json.RawMessage(event.Attachments), "mentions": json.RawMessage(event.Mentions), "emoji": event.Emoji}
			encoded, _ := json.Marshal(content)
			occurred, _ := time.Parse(time.RFC3339Nano, event.Timestamp)
			var occurredAt *time.Time
			if !occurred.IsZero() {
				occurredAt = &occurred
			}
			var deletedAt *time.Time
			if envelope.Type == "MESSAGE_DELETE" || envelope.Type == "THREAD_DELETE" {
				now := time.Now().UTC()
				deletedAt = &now
			}
			if storeErr := s.database.UpsertProviderContentRecord(ctx, db.ProviderContentRecord{SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "discord", ExternalRecordID: externalID, RecordType: strings.ToLower(envelope.Type), Fingerprint: providerPayloadFingerprint(raw), DisplayName: resource.DisplayName + " · " + event.Author.Username, MIMEType: "application/vnd.discord.message+json", OccurredAt: occurredAt, Content: encoded, DeletedAt: deletedAt}); storeErr != nil {
				state = "failed"
			} else {
				_, _ = s.ProcessProviderEvent(ctx, resource, eventID, providerPayloadFingerprint(raw), json.RawMessage(raw))
			}
		}
		_ = s.database.FinishProviderEvent(ctx, resource.IntegrationID, eventID, state)
	}
	return nil
}

func (e discordGatewayEnvelope) SequenceOrZero() int64 {
	if e.Sequence == nil {
		return 0
	}
	return *e.Sequence
}

// mirrorDiscordDispatch fans a live Discord message out to every Space that
// mirrors the channel. Failures are deliberately swallowed: a Space whose
// conversation was deleted must not stall the Gateway for everyone else, and
// the next manual sync will reconcile from the stored cursor regardless.
func (s *SpacesService) mirrorDiscordDispatch(ctx context.Context, envelope discordGatewayEnvelope, guildID, channelID string) {
	if envelope.Type != "MESSAGE_CREATE" {
		return
	}
	links, err := s.database.SpaceDiscordLinksForChannel(ctx, guildID, channelID)
	if err != nil || len(links) == 0 {
		return
	}
	var message TestingDiscordMessage
	if json.Unmarshal(envelope.Data, &message) != nil || message.ID == "" {
		return
	}
	for _, link := range links {
		if !TestingShouldMirrorDiscordMessage(message, &link) {
			continue
		}
		if _, mirrorErr := s.mirrorDiscordMessage(ctx, link, message); mirrorErr != nil {
			continue
		}
		now := time.Now().UTC()
		cursor := ""
		if TestingSnowflakeAfter(message.ID, link.LastMessageID) {
			cursor = message.ID
		}
		_ = s.database.SetSpaceDiscordLinkSync(ctx, link.ID, cursor, "active", "", &now)
	}
}
