package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	socialintegration "github.com/kannachi323/misty/server/internal/integrations/social"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) ProcessSocialAutomations(ctx context.Context, limit int) (int, error) {
	if s.agent == nil || strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_SOCIAL_AUTOMATION_DISABLED")), "true") {
		return 0, nil
	}
	triggers, err := s.database.PendingSocialAutomationTriggers(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, trigger := range triggers {
		if trigger.AuthorKind == "bot" {
			_, _ = s.database.RecordSocialAutomationDecision(ctx, trigger.SocialAutomationCandidate, trigger.TriggerMessageID, "skip", "bot_message", "", 0)
			processed++
			continue
		}
		prompt := fmt.Sprintf(`You are drafting a background reply for Misty Social. Return strict JSON only: {"reply":"...","confidence":0.0,"sensitive":false}. Follow the user's standing instructions, but never claim to be the account owner, never make payments or legal/medical commitments, never expose secrets, and never reply to abusive or ambiguous requests. Mark sensitive true for money, credentials, legal, medical, safety, sexual, or high-impact decisions. Keep the reply concise. Standing instructions: %s\nTone: %s\nIncoming message: %s`, trigger.Instructions, trigger.Tone, trigger.Text)
		text, _, modelErr := s.agent.CompleteWithModelContext(ctx, trigger.UserID, prompt, db.CreditMeterAutomationAI, serveragent.InitialSelectedModelID)
		if modelErr != nil {
			_, _ = s.database.RecordSocialAutomationDecision(ctx, trigger.SocialAutomationCandidate, trigger.TriggerMessageID, "blocked", "model_failed", "", 0)
			continue
		}
		var result struct {
			Reply      string  `json:"reply"`
			Confidence float64 `json:"confidence"`
			Sensitive  bool    `json:"sensitive"`
		}
		start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
		if start < 0 || end < start || json.Unmarshal([]byte(text[start:end+1]), &result) != nil || strings.TrimSpace(result.Reply) == "" {
			_, _ = s.database.RecordSocialAutomationDecision(ctx, trigger.SocialAutomationCandidate, trigger.TriggerMessageID, "draft", "invalid_model_output", "", 0)
			processed++
			continue
		}
		decision, reason := "reply", "approved"
		reply := strings.TrimSpace(result.Reply) + "\n\n— sent with Misty"
		if result.Sensitive {
			decision, reason = "draft", "sensitive"
		} else if result.Confidence < trigger.ConfidenceThreshold {
			decision, reason = "draft", "low_confidence"
		}
		if _, err := s.database.RecordSocialAutomationDecision(ctx, trigger.SocialAutomationCandidate, trigger.TriggerMessageID, decision, reason, reply, result.Confidence); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

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
