package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	socialintegration "github.com/kannachi323/misty/server/internal/integrations/social"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const socialWebhookBodyLimit = 2 << 20

func (s *SpacesService) InstagramSocialWebhook() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("hub.mode") == "subscribe" && hmac.Equal([]byte(r.URL.Query().Get("hub.verify_token")), []byte(strings.TrimSpace(envconfig.Getenv("INSTAGRAM_WEBHOOK_VERIFY_TOKEN")))) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(r.URL.Query().Get("hub.challenge")))
				return
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, socialWebhookBodyLimit+1))
		if err != nil || len(raw) > socialWebhookBodyLimit {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !validInstagramSignature(raw, r.Header.Get("X-Hub-Signature-256")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		messages, err := (socialintegration.InstagramAdapter{}).NormalizeEvent(r.Context(), raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		recipients := instagramRecipientByMessage(raw)
		for _, message := range messages {
			bindings, bindingErr := s.database.SocialBindingsForInbound(r.Context(), "instagram", message.ConversationID, recipients[message.ExternalID])
			if bindingErr != nil {
				continue
			}
			for _, binding := range bindings {
				_, _, _ = s.database.ImportSocialInboundMessage(r.Context(), binding, message.ExternalID, message.Identity.ExternalUserID, message.Identity.DisplayName, message.Identity.Handle, message.Identity.Kind, message.Text, message.CreatedAt, message.Raw)
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

func validInstagramSignature(body []byte, value string) bool {
	secret := strings.TrimSpace(envconfig.Getenv("INSTAGRAM_APP_SECRET"))
	if secret == "" {
		return false
	}
	provided := strings.TrimPrefix(strings.TrimSpace(value), "sha256=")
	decoded, err := hex.DecodeString(provided)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(decoded, mac.Sum(nil))
}

func instagramRecipientByMessage(raw []byte) map[string]string {
	result := map[string]string{}
	var webhook struct {
		Entry []struct {
			Messaging []struct {
				Recipient struct {
					ID string `json:"id"`
				} `json:"recipient"`
				Message struct {
					MID string `json:"mid"`
				} `json:"message"`
			} `json:"messaging"`
		} `json:"entry"`
	}
	if json.Unmarshal(raw, &webhook) != nil {
		return result
	}
	for _, entry := range webhook.Entry {
		for _, item := range entry.Messaging {
			if item.Message.MID != "" {
				result[item.Message.MID] = item.Recipient.ID
			}
		}
	}
	return result
}
