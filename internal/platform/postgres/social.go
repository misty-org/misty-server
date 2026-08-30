package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SocialBinding struct {
	ID                 string          `json:"id"`
	SpaceID            string          `json:"space_id"`
	ConnectionID       string          `json:"connection_id"`
	ConnectedByUserID  string          `json:"connected_by_user_id"`
	ConversationID     string          `json:"conversation_id,omitempty"`
	Provider           string          `json:"provider"`
	ExternalResourceID string          `json:"external_resource_id"`
	ExternalParentID   string          `json:"external_parent_id,omitempty"`
	DisplayName        string          `json:"display_name"`
	Direction          string          `json:"direction"`
	Status             string          `json:"status"`
	SyncCursor         string          `json:"-"`
	LastErrorCode      string          `json:"last_error_code,omitempty"`
	Capabilities       json.RawMessage `json:"capabilities"`
	LastSyncedAt       *time.Time      `json:"last_synced_at,omitempty"`
	DisabledAt         *time.Time      `json:"disabled_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type SocialSendAuthority struct {
	ID              string          `json:"id"`
	SpaceID         string          `json:"space_id"`
	UserID          string          `json:"user_id"`
	ConnectionID    string          `json:"connection_id"`
	BindingID       string          `json:"binding_id,omitempty"`
	Timezone        string          `json:"timezone"`
	AllowManual     bool            `json:"allow_manual"`
	AllowScheduled  bool            `json:"allow_scheduled"`
	AllowAutomation bool            `json:"allow_automation"`
	HourlyLimit     int             `json:"hourly_limit"`
	DailyLimit      int             `json:"daily_limit"`
	QuietHours      json.RawMessage `json:"quiet_hours"`
	ApprovedAt      time.Time       `json:"approved_at"`
	RevokedAt       *time.Time      `json:"revoked_at,omitempty"`
}

type SocialAutomationRule struct {
	ID                   string     `json:"id"`
	SpaceID              string     `json:"space_id"`
	BindingID            string     `json:"binding_id"`
	ConversationID       string     `json:"conversation_id,omitempty"`
	AuthorityID          string     `json:"authority_id"`
	CreatedByUserID      string     `json:"created_by_user_id"`
	Name                 string     `json:"name"`
	Instructions         string     `json:"instructions"`
	Tone                 string     `json:"tone"`
	ConfidenceThreshold  float64    `json:"confidence_threshold"`
	MaxRepliesPerHour    int        `json:"max_replies_per_hour"`
	MaxRepliesPerDay     int        `json:"max_replies_per_day"`
	CooldownSeconds      int        `json:"cooldown_seconds"`
	MaxUnansweredReplies int        `json:"max_unanswered_replies"`
	Enabled              bool       `json:"enabled"`
	PausedAt             *time.Time `json:"paused_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type SocialScheduledMessage struct {
	ID                string          `json:"id"`
	SpaceID           string          `json:"space_id"`
	BindingID         string          `json:"binding_id"`
	ConversationID    string          `json:"conversation_id"`
	AuthorityID       string          `json:"authority_id"`
	CreatedByUserID   string          `json:"created_by_user_id"`
	Content           json.RawMessage `json:"content"`
	ScheduledAt       time.Time       `json:"scheduled_at"`
	Timezone          string          `json:"timezone"`
	Status            string          `json:"status"`
	OutboundCommandID string          `json:"outbound_command_id,omitempty"`
	LastErrorCode     string          `json:"last_error_code,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type SocialOutboundCommand struct {
	ID, SpaceID, BindingID, ConversationID, AuthorityID, RequestedByUserID string
	SourceKind, IdempotencyKey, State, LastErrorCode                       string
	Content, ProviderReceipt                                               json.RawMessage
	Attempts                                                               int
	AvailableAt                                                            time.Time
	LeaseExpiresAt                                                         *time.Time
	CreatedAt, UpdatedAt                                                   time.Time
}

type SocialOutboundDelivery struct {
	SocialOutboundCommand
	Provider, ExternalResourceID, ExternalParentID, ConnectionID, ConnectionUserID string
}

type SocialAutomationCandidate struct {
	RuleID, SpaceID, BindingID, ConversationID, AuthorityID, UserID string
	Instructions, Tone                                              string
	ConfidenceThreshold                                             float64
}

type SocialAutomationTrigger struct {
	SocialAutomationCandidate
	TriggerMessageID, Text, AuthorKind string
}

const socialBindingColumns = `id,space_id,connection_id,connected_by_user_id,
	COALESCE(conversation_id,''),provider,external_resource_id,external_parent_id,
	display_name,direction,status,capabilities,sync_cursor,last_synced_at,
	last_error_code,disabled_at,created_at,updated_at`

func scanSocialBinding(row interface{ Scan(...any) error }, item *SocialBinding) error {
	return row.Scan(&item.ID, &item.SpaceID, &item.ConnectionID, &item.ConnectedByUserID,
		&item.ConversationID, &item.Provider, &item.ExternalResourceID, &item.ExternalParentID,
		&item.DisplayName, &item.Direction, &item.Status, &item.Capabilities, &item.SyncCursor,
		&item.LastSyncedAt, &item.LastErrorCode, &item.DisabledAt, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) SocialBindings(ctx context.Context, userID, spaceID string) ([]SocialBinding, error) {
	items := []SocialBinding{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+socialBindingColumns+` FROM social_bindings
			WHERE space_id=$1 AND disabled_at IS NULL ORDER BY provider,display_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SocialBinding
			if err := scanSocialBinding(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SocialBinding(ctx context.Context, userID, spaceID, bindingID string) (*SocialBinding, error) {
	item := &SocialBinding{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		return scanSocialBinding(tx.QueryRowContext(ctx, `SELECT `+socialBindingColumns+` FROM social_bindings WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, bindingID, spaceID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) CreateSocialBinding(ctx context.Context, userID, spaceID, connectionID, provider, resourceID, parentID, displayName string) (*SocialBinding, error) {
	provider, resourceID, displayName = strings.TrimSpace(provider), strings.TrimSpace(resourceID), strings.TrimSpace(displayName)
	if (provider != "discord" && provider != "instagram") || resourceID == "" || displayName == "" {
		return nil, ErrSpaceInvalid
	}
	item := &SocialBinding{ID: "social_binding_" + uuid.NewString(), SpaceID: spaceID, ConnectionID: connectionID, ConnectedByUserID: userID, Provider: provider, ExternalResourceID: resourceID, ExternalParentID: strings.TrimSpace(parentID), DisplayName: displayName, Direction: "two_way", Status: "active", Capabilities: json.RawMessage(`{"read":true,"send":true,"schedule":true,"automate":true}`)}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var owns bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM connected_accounts WHERE id=$1 AND user_id=$2 AND provider=$3 AND status='active' AND revoked_at IS NULL)`, connectionID, userID, provider).Scan(&owns); err != nil {
			return err
		}
		if !owns {
			return ErrSpaceForbidden
		}
		conversationID := ""
		err := tx.QueryRowContext(ctx, `SELECT id FROM space_conversations WHERE space_id=$1 AND origin=$2 AND external_resource_id=$3`, spaceID, provider, resourceID).Scan(&conversationID)
		if errors.Is(err, sql.ErrNoRows) {
			conversationID = "space_conversation_" + uuid.NewString()
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversations(id,space_id,title,kind,created_by_user_id,origin,integration_id,external_resource_id,external_display_name,integration_status,visible_to_space)
				VALUES($1,$2,$3,'standard',$4,$5,NULL,$6,$3,'active',TRUE) RETURNING id`, conversationID, spaceID, displayName, userID, provider, resourceID).Scan(&conversationID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE space_conversations SET title=$1,external_display_name=$1,integration_status='active',updated_at=NOW() WHERE id=$2`, displayName, conversationID); err != nil {
				return err
			}
		}
		item.ConversationID = conversationID
		query := `INSERT INTO social_bindings(id,space_id,connection_id,connected_by_user_id,conversation_id,provider,external_resource_id,external_parent_id,display_name,direction,status,capabilities)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT(space_id,provider,external_resource_id) DO UPDATE SET connection_id=EXCLUDED.connection_id,connected_by_user_id=EXCLUDED.connected_by_user_id,conversation_id=EXCLUDED.conversation_id,external_parent_id=EXCLUDED.external_parent_id,display_name=EXCLUDED.display_name,status='active',disabled_at=NULL,updated_at=NOW()
			RETURNING ` + socialBindingColumns
		return scanSocialBinding(tx.QueryRowContext(ctx, query, item.ID, spaceID, connectionID, userID, conversationID, provider, resourceID, item.ExternalParentID, displayName, item.Direction, item.Status, item.Capabilities), item)
	})
	return item, err
}

func (db *Database) DisableSocialBinding(ctx context.Context, userID, spaceID, bindingID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE social_bindings SET status='disabled',disabled_at=NOW(),updated_at=NOW() WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, bindingID, spaceID)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) UpsertSocialSendAuthority(ctx context.Context, userID, spaceID, connectionID, bindingID string, manual, scheduled, automation bool) (*SocialSendAuthority, error) {
	item := &SocialSendAuthority{ID: "social_authority_" + uuid.NewString(), SpaceID: spaceID, UserID: userID, ConnectionID: connectionID, BindingID: bindingID, AllowManual: manual, AllowScheduled: scheduled, AllowAutomation: automation, HourlyLimit: 5, DailyLimit: 25, QuietHours: json.RawMessage(`{}`), Timezone: "UTC"}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		query := `INSERT INTO social_send_authorities(id,space_id,user_id,connection_id,binding_id,allow_manual,allow_scheduled,allow_automation)
		VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8) ON CONFLICT(user_id,connection_id,COALESCE(binding_id,'')) WHERE revoked_at IS NULL DO UPDATE SET allow_manual=EXCLUDED.allow_manual,allow_scheduled=EXCLUDED.allow_scheduled,allow_automation=EXCLUDED.allow_automation,approved_at=NOW(),updated_at=NOW()
		RETURNING id,space_id,user_id,connection_id,COALESCE(binding_id,''),allow_manual,allow_scheduled,allow_automation,hourly_limit,daily_limit,quiet_hours,timezone,approved_at,revoked_at`
		return tx.QueryRowContext(ctx, query, item.ID, spaceID, userID, connectionID, bindingID, manual, scheduled, automation).Scan(&item.ID, &item.SpaceID, &item.UserID, &item.ConnectionID, &item.BindingID, &item.AllowManual, &item.AllowScheduled, &item.AllowAutomation, &item.HourlyLimit, &item.DailyLimit, &item.QuietHours, &item.Timezone, &item.ApprovedAt, &item.RevokedAt)
	})
	return item, err
}

func (db *Database) SocialSendAuthorities(ctx context.Context, userID, spaceID string) ([]SocialSendAuthority, error) {
	items := []SocialSendAuthority{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,user_id,connection_id,COALESCE(binding_id,''),allow_manual,allow_scheduled,allow_automation,hourly_limit,daily_limit,quiet_hours,timezone,approved_at,revoked_at FROM social_send_authorities WHERE space_id=$1 AND user_id=$2 AND revoked_at IS NULL ORDER BY approved_at DESC`, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SocialSendAuthority
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.UserID, &item.ConnectionID, &item.BindingID, &item.AllowManual, &item.AllowScheduled, &item.AllowAutomation, &item.HourlyLimit, &item.DailyLimit, &item.QuietHours, &item.Timezone, &item.ApprovedAt, &item.RevokedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SocialAutomationRules(ctx context.Context, userID, spaceID string) ([]SocialAutomationRule, error) {
	items := []SocialAutomationRule{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,binding_id,COALESCE(conversation_id,''),authority_id,created_by_user_id,name,instructions,tone,confidence_threshold,max_replies_per_hour,max_replies_per_day,cooldown_seconds,max_unanswered_replies,enabled,paused_at,created_at,updated_at FROM social_automation_rules WHERE space_id=$1 ORDER BY created_at DESC`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i SocialAutomationRule
			if err := rows.Scan(&i.ID, &i.SpaceID, &i.BindingID, &i.ConversationID, &i.AuthorityID, &i.CreatedByUserID, &i.Name, &i.Instructions, &i.Tone, &i.ConfidenceThreshold, &i.MaxRepliesPerHour, &i.MaxRepliesPerDay, &i.CooldownSeconds, &i.MaxUnansweredReplies, &i.Enabled, &i.PausedAt, &i.CreatedAt, &i.UpdatedAt); err != nil {
				return err
			}
			items = append(items, i)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SaveSocialAutomationRule(ctx context.Context, userID, spaceID string, item SocialAutomationRule) (*SocialAutomationRule, error) {
	if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Instructions) == "" || item.BindingID == "" || item.AuthorityID == "" {
		return nil, ErrSpaceInvalid
	}
	if item.ID == "" {
		item.ID = "social_rule_" + uuid.NewString()
	}
	item.SpaceID = spaceID
	item.CreatedByUserID = userID
	if item.ConfidenceThreshold == 0 {
		item.ConfidenceThreshold = .8
	}
	if item.MaxRepliesPerHour == 0 {
		item.MaxRepliesPerHour = 5
	}
	if item.MaxRepliesPerDay == 0 {
		item.MaxRepliesPerDay = 25
	}
	if item.CooldownSeconds == 0 {
		item.CooldownSeconds = 120
	}
	if item.MaxUnansweredReplies == 0 {
		item.MaxUnansweredReplies = 2
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		query := `INSERT INTO social_automation_rules(id,space_id,binding_id,conversation_id,authority_id,created_by_user_id,name,instructions,tone,confidence_threshold,max_replies_per_hour,max_replies_per_day,cooldown_seconds,max_unanswered_replies,enabled)
		VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,instructions=EXCLUDED.instructions,tone=EXCLUDED.tone,confidence_threshold=EXCLUDED.confidence_threshold,max_replies_per_hour=EXCLUDED.max_replies_per_hour,max_replies_per_day=EXCLUDED.max_replies_per_day,cooldown_seconds=EXCLUDED.cooldown_seconds,max_unanswered_replies=EXCLUDED.max_unanswered_replies,enabled=EXCLUDED.enabled,updated_at=NOW() WHERE social_automation_rules.space_id=EXCLUDED.space_id
		RETURNING created_at,updated_at`
		return tx.QueryRowContext(ctx, query, item.ID, spaceID, item.BindingID, item.ConversationID, item.AuthorityID, userID, item.Name, item.Instructions, item.Tone, item.ConfidenceThreshold, item.MaxRepliesPerHour, item.MaxRepliesPerDay, item.CooldownSeconds, item.MaxUnansweredReplies, item.Enabled).Scan(&item.CreatedAt, &item.UpdatedAt)
	})
	return &item, err
}

func (db *Database) SocialScheduledMessages(ctx context.Context, userID, spaceID string) ([]SocialScheduledMessage, error) {
	items := []SocialScheduledMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,binding_id,conversation_id,authority_id,created_by_user_id,content,scheduled_at,timezone,status,COALESCE(outbound_command_id,''),last_error_code,created_at,updated_at FROM social_scheduled_messages WHERE space_id=$1 ORDER BY scheduled_at`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i SocialScheduledMessage
			if err := rows.Scan(&i.ID, &i.SpaceID, &i.BindingID, &i.ConversationID, &i.AuthorityID, &i.CreatedByUserID, &i.Content, &i.ScheduledAt, &i.Timezone, &i.Status, &i.OutboundCommandID, &i.LastErrorCode, &i.CreatedAt, &i.UpdatedAt); err != nil {
				return err
			}
			items = append(items, i)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) ScheduleSocialMessage(ctx context.Context, userID, spaceID string, item SocialScheduledMessage) (*SocialScheduledMessage, error) {
	if item.BindingID == "" || item.ConversationID == "" || item.AuthorityID == "" || item.ScheduledAt.Before(time.Now().UTC().Add(-time.Minute)) || len(item.Content) == 0 {
		return nil, ErrSpaceInvalid
	}
	item.ID = "social_scheduled_" + uuid.NewString()
	item.SpaceID = spaceID
	item.CreatedByUserID = userID
	if item.Timezone == "" {
		item.Timezone = "UTC"
	}
	item.Status = "scheduled"
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO social_scheduled_messages(id,space_id,binding_id,conversation_id,authority_id,created_by_user_id,content,scheduled_at,timezone) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 WHERE EXISTS(SELECT 1 FROM social_send_authorities WHERE id=$5 AND user_id=$6 AND allow_scheduled AND revoked_at IS NULL) RETURNING created_at,updated_at`, item.ID, spaceID, item.BindingID, item.ConversationID, item.AuthorityID, userID, item.Content, item.ScheduledAt, item.Timezone).Scan(&item.CreatedAt, &item.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return &item, err
}

func (db *Database) CancelSocialScheduledMessage(ctx context.Context, userID, spaceID, id string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE social_scheduled_messages SET status='cancelled',updated_at=NOW() WHERE id=$1 AND space_id=$2 AND created_by_user_id=$3 AND status='scheduled'`, id, spaceID, userID)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}
