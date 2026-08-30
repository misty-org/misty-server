package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	socialintegration "github.com/kannachi323/misty/server/internal/integrations/social"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/gorilla/websocket"
)

type discordGatewayEnvelope struct {
	Op       int             `json:"op"`
	Sequence *int64          `json:"s"`
	Type     string          `json:"t"`
	Data     json.RawMessage `json:"d"`
}

func (s *SpacesService) RunDiscordSocialGateway(ctx context.Context) {
	token := strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN"))
	if token == "" || strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_SOCIAL_DISCORD_DISABLED")), "true") {
		return
	}
	for ctx.Err() == nil {
		if err := s.runDiscordSocialSession(ctx, token); err != nil && ctx.Err() == nil {
			log.Printf("Discord Social gateway disconnected: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *SpacesService) runDiscordSocialSession(ctx context.Context, token string) error {
	endpoint := strings.TrimSpace(envconfig.Getenv("DISCORD_GATEWAY_URL"))
	if endpoint == "" {
		endpoint = "wss://gateway.discord.gg/?v=10&encoding=json"
	}
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
	var hello discordGatewayEnvelope
	if err := connection.ReadJSON(&hello); err != nil {
		return err
	}
	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if hello.Op != 10 || json.Unmarshal(hello.Data, &helloData) != nil || helloData.HeartbeatInterval < 1000 {
		return socialintegration.ErrUnsupportedOperation
	}
	identify := map[string]any{"op": 2, "d": map[string]any{"token": token, "intents": 33281, "properties": map[string]string{"os": "misty", "browser": "misty", "device": "misty"}}}
	if err := connection.WriteJSON(identify); err != nil {
		return err
	}
	_ = connection.SetReadDeadline(time.Time{})
	incoming := make(chan discordGatewayEnvelope, 16)
	failures := make(chan error, 1)
	go func() {
		for {
			var event discordGatewayEnvelope
			if err := connection.ReadJSON(&event); err != nil {
				failures <- err
				return
			}
			incoming <- event
		}
	}()
	ticker := time.NewTicker(time.Duration(helloData.HeartbeatInterval) * time.Millisecond)
	defer ticker.Stop()
	var sequence *int64
	for {
		select {
		case <-ctx.Done():
			_ = connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
			return nil
		case err := <-failures:
			return err
		case <-ticker.C:
			if err := connection.WriteJSON(map[string]any{"op": 1, "d": sequence}); err != nil {
				return err
			}
		case event := <-incoming:
			if event.Sequence != nil {
				value := *event.Sequence
				sequence = &value
			}
			if event.Op == 7 {
				return nil
			}
			if event.Op != 0 || event.Type != "MESSAGE_CREATE" {
				continue
			}
			raw, _ := json.Marshal(map[string]any{"t": event.Type, "d": json.RawMessage(event.Data)})
			messages, normalizeErr := (socialintegration.DiscordAdapter{}).NormalizeEvent(ctx, raw)
			if normalizeErr != nil {
				continue
			}
			for _, message := range messages {
				bindings, bindingErr := s.database.SocialBindingsForInbound(ctx, "discord", message.ConversationID, "")
				if bindingErr != nil {
					continue
				}
				for _, binding := range bindings {
					_, _, _ = s.database.ImportSocialInboundMessage(ctx, binding, message.ExternalID, message.Identity.ExternalUserID, message.Identity.DisplayName, message.Identity.Handle, message.Identity.Kind, message.Text, message.CreatedAt, message.Raw)
				}
			}
		}
	}
}
