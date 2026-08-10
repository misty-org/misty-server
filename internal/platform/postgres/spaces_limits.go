package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	MaxSpaceNodes        = 5000
	MaxMessageChars      = 4000
	MaxMessageFiles      = 5
	MaxSpaceStorageBytes = int64(1_000_000_000)
)

var (
	ErrSpaceNotFound        = errors.New("space not found")
	ErrSpaceForbidden       = errors.New("space permission denied")
	ErrSpaceLimit           = errors.New("space limit reached")
	ErrSpaceOwnershipLimit  = errors.New("space ownership limit reached")
	ErrSpacePeopleLimit     = errors.New("space member limit reached")
	ErrSpaceNodeLimit       = errors.New("space node limit reached")
	ErrSpaceConflict        = errors.New("space resource version conflict")
	ErrSpaceInviteNotFound  = errors.New("space invitation not found")
	ErrSpaceInviteeNotFound = errors.New("no misty account found for invitee email")
	ErrSpaceInviteExpired   = errors.New("space invitation expired")
	ErrSpaceInvalid         = errors.New("invalid space data")
)

type Space struct {
	ID               string          `json:"id"`
	SecurityDomainID string          `json:"security_domain_id"`
	OwnerUserID      string          `json:"owner_user_id"`
	Name             string          `json:"name"`
	Kind             string          `json:"kind"`
	Role             string          `json:"role"`
	MemberCount      int             `json:"member_count"`
	PendingCount     int             `json:"pending_count"`
	IsShared         bool            `json:"is_shared"`
	Permissions      map[string]bool `json:"permissions"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type SpaceMember struct {
	SpaceID  string    `json:"space_id"`
	UserID   string    `json:"user_id"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
	ReadSeq  int64     `json:"read_message_seq"`
}

type SpaceInvitation struct {
	ID              string    `json:"id"`
	SpaceID         string    `json:"space_id"`
	SpaceName       string    `json:"space_name"`
	InvitedUserID   *string   `json:"invited_user_id"`
	InvitedUserName *string   `json:"invited_user_name"`
	InvitedEmail    string    `json:"invited_email"`
	InvitedByUserID string    `json:"invited_by_user_id"`
	InviterName     string    `json:"inviter_name,omitempty"`
	DeliveryStatus  string    `json:"delivery_status"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type SpaceInvitationPreview struct {
	SpaceName    string    `json:"space_name"`
	InviterName  string    `json:"inviter_name"`
	InvitedEmail string    `json:"invited_email"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type MessageSpan struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	UserID  string `json:"user_id,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Label   string `json:"label,omitempty"`
	URL     string `json:"url,omitempty"`
}

type SpaceMessage struct {
	Seq                 int64                  `json:"seq"`
	ID                  string                 `json:"id"`
	SpaceID             string                 `json:"space_id"`
	ConversationID      string                 `json:"conversation_id,omitempty"`
	SenderUserID        string                 `json:"sender_user_id"`
	SenderName          string                 `json:"sender_name"`
	SenderAvatarVersion int64                  `json:"sender_avatar_version,omitempty"`
	SenderKind          string                 `json:"sender_kind"`
	SenderAgentID       string                 `json:"sender_agent_id,omitempty"`
	Sender              SpaceMessageSender     `json:"sender"`
	Content             []MessageSpan          `json:"content"`
	FileNodeIDs         []string               `json:"file_node_ids"`
	LibraryItemIDs      []string               `json:"library_item_ids"`
	Attachments         []MessageAttachment    `json:"attachments"`
	Reactions           []SpaceMessageReaction `json:"reactions"`
	ReplyToMessageID    string                 `json:"reply_to_message_id,omitempty"`
	EditedAt            *time.Time             `json:"edited_at,omitempty"`
	// Provenance for a mirrored message. Absent means Misty-native chat, so
	// every existing client stays valid and "no origin" reads as "ours".
	Origin    json.RawMessage `json:"origin,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type SpaceMessageSender struct {
	Kind          string `json:"kind"`
	UserID        string `json:"user_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	DisplayName   string `json:"display_name"`
	AvatarVersion int64  `json:"avatar_version,omitempty"`
}

type SpaceMessageReaction struct {
	Emoji       string `json:"emoji"`
	Count       int    `json:"count"`
	ReactedByMe bool   `json:"reacted_by_me,omitempty"`
}

type SpaceNode struct {
	ID             string          `json:"id"`
	SpaceID        string          `json:"space_id"`
	ParentID       string          `json:"parent_id,omitempty"`
	Kind           string          `json:"kind"`
	DisplayName    string          `json:"display_name"`
	UploaderUserID string          `json:"uploader_user_id"`
	MIMEType       string          `json:"mime_type"`
	SizeBytes      *int64          `json:"size_bytes,omitempty"`
	Stale          bool            `json:"stale"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	TargetCipher   []byte          `json:"-"`
	TargetNonce    []byte          `json:"-"`
	KeyVersion     int16           `json:"-"`
}

type SpaceEvent struct {
	ID          int64           `json:"id"`
	SpaceID     string          `json:"space_id"`
	EventType   string          `json:"type"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	EntityID    string          `json:"entity_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
}

type SpaceInboxItem struct {
	ID        int64           `json:"id"`
	SpaceID   string          `json:"space_id"`
	SpaceName string          `json:"space_name"`
	Kind      string          `json:"kind"`
	MessageID string          `json:"message_id,omitempty"`
	EventID   *int64          `json:"event_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	SeenAt    *time.Time      `json:"seen_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type SpaceStudioResource struct {
	ID                      string           `json:"id"`
	SpaceID                 string           `json:"space_id"`
	CreatorUserID           string           `json:"creator_user_id"`
	Kind                    string           `json:"kind"`
	Name                    string           `json:"name"`
	Description             string           `json:"description,omitempty"`
	Icon                    string           `json:"icon,omitempty"`
	ModelMode               string           `json:"model_mode,omitempty"`
	ModelID                 string           `json:"model_id,omitempty"`
	Instructions            string           `json:"instructions,omitempty"`
	Definition              json.RawMessage  `json:"definition,omitempty"`
	Enabled                 bool             `json:"enabled"`
	Status                  string           `json:"status,omitempty"`
	RuntimeKind             string           `json:"runtime_kind,omitempty"`
	Version                 int64            `json:"version"`
	SchedulesEnabled        bool             `json:"schedules_enabled"`
	StableIdentifier        string           `json:"stable_identifier,omitempty"`
	ActiveWorkflowVersionID string           `json:"active_workflow_version_id,omitempty"`
	ActiveWorkflow          *WorkflowVersion `json:"active_workflow,omitempty"`
	AccessPolicy            json.RawMessage  `json:"access_policy,omitempty"`
	CreatedAt               time.Time        `json:"created_at"`
	UpdatedAt               time.Time        `json:"updated_at"`
}

type SpaceRun struct {
	ID                    string          `json:"id"`
	SpaceID               string          `json:"space_id"`
	ResourceKind          string          `json:"resource_kind"`
	ResourceID            string          `json:"resource_id"`
	InitiatedByUserID     string          `json:"initiated_by_user_id"`
	BillingUserID         string          `json:"billing_user_id"`
	TriggerKind           string          `json:"trigger_kind"`
	State                 string          `json:"state"`
	Input                 json.RawMessage `json:"input"`
	Result                json.RawMessage `json:"result"`
	ErrorCode             string          `json:"error_code,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	CompletedAt           *time.Time      `json:"completed_at,omitempty"`
	RequestingMemberID    string          `json:"requesting_member_id"`
	SourceConversationID  string          `json:"source_conversation_id,omitempty"`
	ConversationScopeKind string          `json:"conversation_scope_kind"`
	ScopeConversationID   string          `json:"scope_conversation_id,omitempty"`
	SourceMessageID       string          `json:"source_message_id,omitempty"`
	SourceType            string          `json:"source_type"`
	AgentID               string          `json:"agent_id,omitempty"`
	WorkflowIdentifier    string          `json:"workflow_identifier,omitempty"`
	WorkflowVersionID     string          `json:"workflow_version_id,omitempty"`
	WorkflowVersion       string          `json:"workflow_version,omitempty"`
	CapabilityID          string          `json:"capability_id,omitempty"`
	Progress              int             `json:"progress"`
	Outputs               json.RawMessage `json:"outputs"`
	Artifacts             json.RawMessage `json:"artifacts"`
	ErrorMessage          string          `json:"error_message,omitempty"`
	RetryOfRunID          string          `json:"retry_of_run_id,omitempty"`
	CanceledAt            *time.Time      `json:"canceled_at,omitempty"`
	UpdatedAt             time.Time       `json:"updated_at"`
	AgentInstanceID       string          `json:"agent_instance_id,omitempty"`
	AgentVersionID        string          `json:"agent_version_id,omitempty"`
	Attempt               int             `json:"attempt"`
	NextRetryAt           *time.Time      `json:"next_retry_at,omitempty"`
	SourceTaskID          string          `json:"source_task_id,omitempty"`
	ActionEnvelope        json.RawMessage `json:"action_envelope,omitempty"`
}

func (db *Database) TestingSpaceTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), fn)
}

func normalizeSpaceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 80 {
		return "", ErrSpaceInvalid
	}
	return name, nil
}

func requireSpaceMemberTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) (string, error) {
	var role string
	err := tx.QueryRowContext(ctx, `SELECT m.role FROM space_members m JOIN spaces s ON s.id=m.space_id WHERE m.space_id=$1 AND m.user_id=$2 AND s.lifecycle_state='active'`, spaceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceForbidden
	}
	return role, err
}
