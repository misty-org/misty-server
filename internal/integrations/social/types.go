package social

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type SocialProviderID string

const (
	SocialProviderMisty     SocialProviderID = "misty"
	SocialProviderDiscord   SocialProviderID = "discord"
	SocialProviderInstagram SocialProviderID = "instagram"
)

type SocialConnection struct {
	ID, UserID, AccountID, AccountDisplay string
	Provider                              SocialProviderID
	Capabilities                          []string
	Status                                string
}

type SocialBinding struct {
	ID, SpaceID, ConnectionID, ConversationID string
	Provider                                  SocialProviderID
	ExternalResourceID, ExternalParentID      string
	DisplayName, Direction, Status            string
	Capabilities                              SocialCapabilitySet
}

type SocialConversation struct {
	ID, SpaceID, Title, ExternalResourceID string
	Provider                               SocialProviderID
	BindingID                              string
}

type SocialIdentity struct {
	ID, BindingID, ExternalUserID, DisplayName, Handle, AvatarURL, Kind string
	Provider                                                            SocialProviderID
}

type SocialMessageDirection string

const (
	SocialMessageInbound  SocialMessageDirection = "inbound"
	SocialMessageOutbound SocialMessageDirection = "outbound"
)

type SocialDeliveryState string

const (
	SocialDeliveryQueued    SocialDeliveryState = "queued"
	SocialDeliverySending   SocialDeliveryState = "sending"
	SocialDeliverySent      SocialDeliveryState = "sent"
	SocialDeliveryDelivered SocialDeliveryState = "delivered"
	SocialDeliveryRead      SocialDeliveryState = "read"
	SocialDeliveryFailed    SocialDeliveryState = "failed"
	SocialDeliveryCancelled SocialDeliveryState = "cancelled"
)

type SocialCapabilitySet struct {
	Read, Send, Schedule, Automate, DeliveryReceipts bool
}

type SocialMessage struct {
	ID, ConversationID, ExternalID, ReplyToExternalID string
	Provider                                          SocialProviderID
	Direction                                         SocialMessageDirection
	DeliveryState                                     SocialDeliveryState
	Identity                                          SocialIdentity
	Text                                              string
	Attachments                                       []SocialAttachment
	CreatedAt                                         time.Time
	Raw                                               json.RawMessage
}

type SocialAttachment struct {
	ID, Kind, URL, Name, MIMEType string
	SizeBytes                     int64
}

type SocialAutomationRule struct {
	ID, SpaceID, BindingID, ConversationID, AuthorityID string
	Name, Instructions, Tone                            string
	ConfidenceThreshold                                 float64
	MaxRepliesPerHour, MaxRepliesPerDay                 int
	Cooldown                                            time.Duration
	MaxUnansweredReplies                                int
	Enabled                                             bool
}

type SocialSendAuthority struct {
	ID, SpaceID, UserID, ConnectionID, BindingID, Timezone string
	AllowManual, AllowScheduled, AllowAutomation           bool
	HourlyLimit, DailyLimit                                int
	RevokedAt                                              *time.Time
}

type SocialScheduledMessage struct {
	ID, SpaceID, BindingID, ConversationID, AuthorityID string
	Content                                             []SocialContentSpan
	ScheduledAt                                         time.Time
	Timezone, Status                                    string
}

type SocialAutomationRun struct {
	ID, RuleID, TriggerMessageID, OutboundCommandID string
	Decision, ReasonCode                            string
	Confidence                                      float64
	DraftContent                                    []SocialContentSpan
}

type SocialOutboundCommand struct {
	ID, SpaceID, BindingID, ConversationID, AuthorityID string
	Provider                                            SocialProviderID
	ExternalResourceID, ExternalParentID                string
	SourceKind                                          string
	Content                                             []SocialContentSpan
	IdempotencyKey                                      string
}

type SocialContentSpan struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type SocialSendReceipt struct {
	ExternalID, State string
	Raw               json.RawMessage
}

type SocialProviderAdapter interface {
	Provider() SocialProviderID
	Capabilities() SocialCapabilitySet
	DiscoverResources(context.Context, string) ([]SocialResource, error)
	NormalizeEvent(context.Context, []byte) ([]SocialMessage, error)
	Send(context.Context, string, SocialOutboundCommand) (SocialSendReceipt, error)
}

type SocialResource struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

var ErrUnsupportedOperation = errors.New("social provider operation is not supported")

func PlainText(content []SocialContentSpan) string {
	parts := make([]string, 0, len(content))
	for _, span := range content {
		if span.Type == "text" && strings.TrimSpace(span.Text) != "" {
			parts = append(parts, span.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}
