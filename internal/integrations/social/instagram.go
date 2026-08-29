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

type InstagramAdapter struct {
	Client  *http.Client
	APIBase string
}

func (InstagramAdapter) Provider() SocialProviderID { return SocialProviderInstagram }
func (InstagramAdapter) Capabilities() SocialCapabilitySet {
	return SocialCapabilitySet{Read: true, Send: true, Schedule: true, Automate: true, DeliveryReceipts: true}
}

func (a InstagramAdapter) DiscoverResources(ctx context.Context, token string) ([]SocialResource, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+"/me?fields=id,username&access_token="+url.QueryEscape(token), nil)
	response, err := a.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var profile struct{ ID, Username string }
	if response.StatusCode < 200 || response.StatusCode >= 300 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profile) != nil || profile.ID == "" {
		return nil, fmt.Errorf("instagram account discovery failed")
	}
	conversationURL := a.base() + "/" + url.PathEscape(profile.ID) + "/conversations?platform=instagram&fields=id,participants&access_token=" + url.QueryEscape(token)
	conversationRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, conversationURL, nil)
	conversationResponse, err := a.client().Do(conversationRequest)
	if err != nil {
		return nil, err
	}
	defer conversationResponse.Body.Close()
	var result struct {
		Data []struct {
			ID           string `json:"id"`
			Participants struct {
				Data []struct {
					ID       string `json:"id"`
					Username string `json:"username"`
					Name     string `json:"name"`
				} `json:"data"`
			} `json:"participants"`
		} `json:"data"`
	}
	if conversationResponse.StatusCode < 200 || conversationResponse.StatusCode >= 300 || json.NewDecoder(io.LimitReader(conversationResponse.Body, 1<<20)).Decode(&result) != nil {
		return nil, fmt.Errorf("instagram conversation discovery failed")
	}
	resources := []SocialResource{}
	for _, conversation := range result.Data {
		for _, participant := range conversation.Participants.Data {
			if participant.ID == profile.ID {
				continue
			}
			name := participant.Username
			if name == "" {
				name = participant.Name
			}
			if name == "" {
				name = participant.ID
			}
			resources = append(resources, SocialResource{ID: participant.ID, ParentID: profile.ID, Name: name, Kind: "conversation"})
		}
	}
	return resources, nil
}

func (InstagramAdapter) NormalizeEvent(_ context.Context, raw []byte) ([]SocialMessage, error) {
	var webhook struct {
		Entry []struct {
			Messaging []struct {
				Sender struct {
					ID string `json:"id"`
				} `json:"sender"`
				Recipient struct {
					ID string `json:"id"`
				} `json:"recipient"`
				Timestamp int64 `json:"timestamp"`
				Message   struct {
					MID, Text string
					IsEcho    bool `json:"is_echo"`
				} `json:"message"`
			} `json:"messaging"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(raw, &webhook); err != nil {
		return nil, err
	}
	messages := []SocialMessage{}
	for _, entry := range webhook.Entry {
		for _, item := range entry.Messaging {
			if item.Message.MID == "" || item.Message.Text == "" || item.Message.IsEcho {
				continue
			}
			messages = append(messages, SocialMessage{ExternalID: item.Message.MID, ConversationID: item.Sender.ID,
				Provider: SocialProviderInstagram, Direction: SocialMessageInbound, DeliveryState: SocialDeliveryDelivered,
				Text: item.Message.Text, CreatedAt: time.UnixMilli(item.Timestamp).UTC(),
				Identity: SocialIdentity{Provider: SocialProviderInstagram, ExternalUserID: item.Sender.ID, Kind: "person"}, Raw: append(json.RawMessage(nil), raw...)})
		}
	}
	return messages, nil
}

func (a InstagramAdapter) Send(ctx context.Context, token string, command SocialOutboundCommand) (SocialSendReceipt, error) {
	text := PlainText(command.Content)
	if token == "" || command.ExternalResourceID == "" || command.ExternalParentID == "" || text == "" {
		return SocialSendReceipt{}, fmt.Errorf("instagram send is missing a token, account, recipient, or content")
	}
	payload, _ := json.Marshal(map[string]any{"recipient": map[string]string{"id": command.ExternalResourceID}, "message": map[string]string{"text": text}})
	endpoint := a.base() + "/" + url.PathEscape(command.ExternalParentID) + "/messages?access_token=" + url.QueryEscape(token)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client().Do(request)
	if err != nil {
		return SocialSendReceipt{}, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SocialSendReceipt{}, fmt.Errorf("instagram send returned %s", response.Status)
	}
	var result struct {
		MessageID string `json:"message_id"`
	}
	if json.Unmarshal(body, &result) != nil || result.MessageID == "" {
		return SocialSendReceipt{}, fmt.Errorf("instagram send returned an invalid receipt")
	}
	return SocialSendReceipt{ExternalID: result.MessageID, State: "sent", Raw: body}, nil
}
func (a InstagramAdapter) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}
func (a InstagramAdapter) base() string {
	if a.APIBase != "" {
		return strings.TrimRight(a.APIBase, "/")
	}
	return "https://graph.instagram.com/v23.0"
}
