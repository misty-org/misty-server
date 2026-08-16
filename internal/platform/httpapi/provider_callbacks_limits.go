package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const providerCallbackBodyLimit = 2 << 20

func (s *SpacesService) SlackEventsCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := readProviderCallbackBody(r)
		if err != nil || !TestingVerifySlackRequest(raw, r.Header, time.Now().UTC(), strings.TrimSpace(envconfig.Getenv("SLACK_SIGNING_SECRET"))) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_signature"})
			return
		}
		var envelope slackEventEnvelope
		if json.Unmarshal(raw, &envelope) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_payload"})
			return
		}
		if envelope.Type == "url_verification" {
			if envelope.Challenge == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "challenge_missing"})
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, envelope.Challenge)
			return
		}
		w.WriteHeader(http.StatusOK)
		payload := append([]byte(nil), raw...)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			_ = s.processSlackEvent(ctx, envelope, payload)
		}()
	}
}

type slackEventEnvelope struct {
	Type      string `json:"type"`
	EventID   string `json:"event_id"`
	TeamID    string `json:"team_id"`
	Challenge string `json:"challenge"`
	Event     struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		Channel   string          `json:"channel"`
		User      string          `json:"user"`
		BotID     string          `json:"bot_id"`
		Text      string          `json:"text"`
		Timestamp string          `json:"ts"`
		ThreadTS  string          `json:"thread_ts"`
		EventTS   string          `json:"event_ts"`
		DeletedTS string          `json:"deleted_ts"`
		Files     json.RawMessage `json:"files"`
		Message   json.RawMessage `json:"message"`
		Previous  json.RawMessage `json:"previous_message"`
	} `json:"event"`
}

func (s *SpacesService) processSlackEvent(ctx context.Context, envelope slackEventEnvelope, raw []byte) error {
	if envelope.EventID == "" || envelope.Event.Channel == "" {
		return errors.New("slack event identity is missing")
	}
	resources, err := s.database.MatchingProviderResources(ctx, "slack", envelope.TeamID, envelope.Event.Channel)
	if err != nil {
		return err
	}
	for _, resource := range resources {
		claimed, claimErr := s.database.EnqueueProviderEvent(ctx, resource, envelope.EventID, raw)
		if claimErr != nil || !claimed {
			continue
		}
		state := "processed"
		if err := s.storeSlackEvent(ctx, resource, envelope, raw); err != nil {
			state = "failed"
		} else {
			if link, linkErr := s.database.SpaceSlackLinkForResource(ctx, resource.ID); linkErr == nil {
				if _, mirrorErr := s.mirrorSlackMessage(ctx, *link, slackChatMessageFromEvent(envelope)); mirrorErr != nil {
					state = "failed"
				}
			}
			if state == "processed" {
				_, _ = s.ProcessProviderEvent(ctx, resource, envelope.EventID, providerPayloadFingerprint(raw), json.RawMessage(raw))
			}
		}
		_ = s.database.FinishProviderEvent(ctx, resource.IntegrationID, envelope.EventID, state)
	}
	return nil
}

func slackChatMessageFromEvent(envelope slackEventEnvelope) SlackChatMessage {
	event := envelope.Event
	result := SlackChatMessage{Type: event.Type, Subtype: event.Subtype, UserID: event.User,
		BotID: event.BotID, Text: event.Text, Timestamp: event.Timestamp,
		ThreadTimestamp: event.ThreadTS}
	if event.Subtype == "message_changed" && len(event.Message) > 0 {
		var changed struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			User     string `json:"user"`
			BotID    string `json:"bot_id"`
			Text     string `json:"text"`
			TS       string `json:"ts"`
			ThreadTS string `json:"thread_ts"`
			Files    []struct {
				Name       string `json:"name"`
				URLPrivate string `json:"url_private"`
				Permalink  string `json:"permalink"`
			} `json:"files"`
		}
		if json.Unmarshal(event.Message, &changed) == nil && changed.TS != "" {
			result.Type, result.Subtype = firstNonempty(changed.Type, "message"), changed.Subtype
			result.UserID, result.BotID, result.Text = changed.User, changed.BotID, changed.Text
			result.Timestamp, result.ThreadTimestamp = changed.TS, changed.ThreadTS
			for _, file := range changed.Files {
				result.Files = append(result.Files, SlackChatFile{Name: file.Name,
					URL: firstNonempty(file.Permalink, file.URLPrivate)})
			}
		}
	}
	if event.DeletedTS != "" || event.Subtype == "message_deleted" {
		result.Type, result.Subtype = "message", ""
		result.Timestamp, result.Text, result.Deleted = firstNonempty(event.DeletedTS, result.Timestamp),
			"Message deleted in Slack", true
	}
	return result
}

func (s *SpacesService) storeSlackEvent(ctx context.Context, resource db.ProviderSharedResource, envelope slackEventEnvelope, raw []byte) error {
	event := envelope.Event
	if event.Subtype == "message_changed" && len(event.Message) > 0 {
		var changed struct {
			Timestamp string          `json:"ts"`
			ThreadTS  string          `json:"thread_ts"`
			User      string          `json:"user"`
			BotID     string          `json:"bot_id"`
			Text      string          `json:"text"`
			Files     json.RawMessage `json:"files"`
		}
		if json.Unmarshal(event.Message, &changed) == nil && changed.Timestamp != "" {
			event.Timestamp, event.ThreadTS, event.User, event.BotID, event.Text = changed.Timestamp, changed.ThreadTS, changed.User, changed.BotID, changed.Text
			if len(changed.Files) > 0 {
				event.Files = changed.Files
			}
		}
	}
	externalID := event.Timestamp
	if event.DeletedTS != "" {
		externalID = event.DeletedTS
	}
	if externalID == "" {
		externalID = event.EventTS
	}
	if externalID == "" {
		return errors.New("slack message identity is missing")
	}
	content := map[string]any{"type": event.Type, "subtype": event.Subtype, "channel": event.Channel, "user": event.User, "bot_id": event.BotID, "text": event.Text, "ts": externalID, "thread_ts": event.ThreadTS}
	if len(event.Files) > 0 {
		content["files"] = json.RawMessage(event.Files)
	}
	if len(event.Message) > 0 {
		content["message"] = json.RawMessage(event.Message)
	}
	if len(event.Previous) > 0 {
		content["previous_message"] = json.RawMessage(event.Previous)
	}
	encoded, _ := json.Marshal(content)
	occurredAt := slackTimestamp(externalID)
	var deletedAt *time.Time
	if event.Subtype == "message_deleted" || event.Type == "message_deleted" {
		now := time.Now().UTC()
		deletedAt = &now
	}
	return s.database.UpsertProviderContentRecord(ctx, db.ProviderContentRecord{SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "slack", ExternalRecordID: externalID, ParentExternalID: event.ThreadTS, RecordType: "message", Fingerprint: providerPayloadFingerprint(raw), DisplayName: resource.DisplayName + " · " + event.User, MIMEType: "application/vnd.slack.message+json", OccurredAt: occurredAt, Content: encoded, DeletedAt: deletedAt})
}

func TestingVerifySlackRequest(raw []byte, headers http.Header, now time.Time, secret string) bool {
	if secret == "" {
		return false
	}
	timestamp := headers.Get("X-Slack-Request-Timestamp")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":"))
	_, _ = mac.Write(raw)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(headers.Get("X-Slack-Signature")))
}

func slackTimestamp(value string) *time.Time {
	parts := strings.SplitN(value, ".", 2)
	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil
	}
	valueTime := time.Unix(seconds, 0).UTC()
	return &valueTime
}

func (s *SpacesService) NotionEventsCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := readProviderCallbackBody(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_payload"})
			return
		}
		var probe map[string]any
		_ = json.Unmarshal(raw, &probe)
		if verification, _ := probe["verification_token"].(string); verification != "" {
			// Notion requires the owner to paste this one-time value back into its
			// provider console. Logging is an explicit bootstrap-only escape hatch;
			// it is disabled by default and must be removed after setup.
			if TestingLogNotionVerificationToken() {
				log.Printf("MISTY_NOTION_WEBHOOK_VERIFICATION_TOKEN=%s", verification)
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		secret := strings.TrimSpace(envconfig.Getenv("NOTION_WEBHOOK_VERIFICATION_TOKEN"))
		if !TestingVerifyNotionRequest(raw, r.Header.Get("X-Notion-Signature"), secret) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_signature"})
			return
		}
		var event notionWebhookEvent
		if json.Unmarshal(raw, &event) != nil || event.ID == "" || event.Entity.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_payload"})
			return
		}
		w.WriteHeader(http.StatusOK)
		payload := append([]byte(nil), raw...)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_ = s.processNotionEvent(ctx, event, payload)
		}()
	}
}

func TestingLogNotionVerificationToken() bool {
	switch strings.ToLower(strings.TrimSpace(envconfig.Getenv("NOTION_WEBHOOK_LOG_VERIFICATION_TOKEN"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type notionWebhookEvent struct {
	ID          string `json:"id"`
	Timestamp   string `json:"timestamp"`
	WorkspaceID string `json:"workspace_id"`
	Type        string `json:"type"`
	Entity      struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"entity"`
	Data struct {
		Parent struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"parent"`
	} `json:"data"`
}

func TestingVerifyNotionRequest(raw []byte, signature, secret string) bool {
	if secret == "" || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}
