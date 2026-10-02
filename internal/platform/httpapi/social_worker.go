package api

import (
	"context"
	"encoding/json"
	"strings"

	socialintegration "github.com/kannachi323/misty/server/internal/integrations/social"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func (s *SpacesService) ProcessSocialDelivery(ctx context.Context, limit int) (int, error) {
	if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_SOCIAL_SEND_DISABLED")), "true") {
		return 0, nil
	}
	if _, err := s.database.QueueDueSocialScheduledMessages(ctx, limit); err != nil {
		return 0, err
	}
	commands, err := s.database.ClaimSocialOutboundCommands(ctx, "social-delivery", limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, command := range commands {
		var content []socialintegration.SocialContentSpan
		if json.Unmarshal(command.Content, &content) != nil {
			_ = s.database.FailSocialOutboundCommand(ctx, command.ID, "invalid_content", false)
			continue
		}
		outbound := socialintegration.SocialOutboundCommand{ID: command.ID, SpaceID: command.SpaceID, BindingID: command.BindingID, ConversationID: command.ConversationID, ExternalResourceID: command.ExternalResourceID, ExternalParentID: command.ExternalParentID, SourceKind: command.SourceKind, Content: content, IdempotencyKey: command.IdempotencyKey}
		var adapter socialintegration.SocialProviderAdapter
		token := ""
		switch command.Provider {
		case "discord":
			adapter = socialintegration.DiscordAdapter{}
			token = strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN"))
		case "instagram":
			adapter = socialintegration.InstagramAdapter{
				APIBase: strings.TrimSpace(envconfig.Getenv("INSTAGRAM_GRAPH_API_BASE_URL")),
			}
			token, _, err = s.connectedAccountAccessTokenForCapability(ctx, command.ConnectionUserID, command.ConnectionID, "social_send")
		default:
			err = socialintegration.ErrUnsupportedOperation
		}
		if err == nil && token == "" {
			err = socialintegration.ErrUnsupportedOperation
		}
		if err != nil {
			_ = s.database.FailSocialOutboundCommand(ctx, command.ID, "provider_not_configured", false)
			continue
		}
		if err := s.database.ValidateSocialOutboundDelivery(ctx, command.ID); err != nil {
			continue
		}
		receipt, sendErr := adapter.Send(ctx, token, outbound)
		if sendErr != nil {
			_ = s.database.FailSocialOutboundCommand(ctx, command.ID, "provider_send_failed", command.Attempts < 4)
			continue
		}
		raw := receipt.Raw
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if err := s.database.CompleteSocialOutboundCommand(ctx, command.ID, receipt.ExternalID, raw); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}
