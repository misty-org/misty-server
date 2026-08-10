package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SpaceDiscordLink binds one Space conversation to one Discord channel.
//
// No Discord token is stored here beyond the optional webhook secret, which is
// sealed with the same AEAD as every other provider credential. The bot token
// itself stays in the server environment and never reaches a client.
type SpaceDiscordLink struct {
	ID                string     `json:"id"`
	SpaceID           string     `json:"space_id"`
	IntegrationID     string     `json:"integration_id"`
	ConversationID    string     `json:"conversation_id,omitempty"`
	ConnectedByUserID string     `json:"connected_by_user_id"`
	GuildID           string     `json:"guild_id"`
	GuildName         string     `json:"guild_name"`
	ChannelID         string     `json:"channel_id"`
	ChannelName       string     `json:"channel_name"`
	Direction         string     `json:"direction"`
	Status            string     `json:"status"`
	LastMessageID     string     `json:"last_message_id,omitempty"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	LastErrorCode     string     `json:"last_error_code,omitempty"`
	BotUserID         string     `json:"bot_user_id,omitempty"`
	WebhookID         string     `json:"-"`
	WebhookCiphertext []byte     `json:"-"`
	WebhookNonce      []byte     `json:"-"`
	DisabledAt        *time.Time `json:"disabled_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// MessageOrigin is the provenance stamped on a mirrored message.
type MessageOrigin struct {
	System            string   `json:"system"`
	ExternalID        string   `json:"external_id,omitempty"`
	ExternalChannelID string   `json:"external_channel_id,omitempty"`
	AuthorName        string   `json:"author_name,omitempty"`
	AuthorHandle      string   `json:"author_handle,omitempty"`
	AuthorAvatarURL   string   `json:"author_avatar_url,omitempty"`
	AuthoredAt        string   `json:"authored_at,omitempty"`
	PublishState      string   `json:"publish_state,omitempty"`
	PublishedAt       string   `json:"published_at,omitempty"`
	PublishedExternal string   `json:"published_external_id,omitempty"`
	PublishError      string   `json:"publish_error,omitempty"`
	AttachmentURLs    []string `json:"attachment_urls,omitempty"`
}

const spaceDiscordLinkColumns = `id,space_id,integration_id,COALESCE(conversation_id,''),connected_by_user_id,guild_id,guild_name,channel_id,channel_name,direction,status,last_message_id,last_synced_at,last_error_code,bot_user_id,webhook_id,webhook_token_ciphertext,webhook_token_nonce,disabled_at,created_at,updated_at`

func scanSpaceDiscordLink(scanner interface{ Scan(...any) error }, out *SpaceDiscordLink) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.IntegrationID, &out.ConversationID, &out.ConnectedByUserID,
		&out.GuildID, &out.GuildName, &out.ChannelID, &out.ChannelName, &out.Direction, &out.Status,
		&out.LastMessageID, &out.LastSyncedAt, &out.LastErrorCode, &out.BotUserID, &out.WebhookID,
		&out.WebhookCiphertext, &out.WebhookNonce, &out.DisabledAt, &out.CreatedAt, &out.UpdatedAt)
}

// SpaceDiscordLinkFor returns the Space's active link, or nil when none exists.
// A Space with no Discord link is the normal case, not an error.
func (db *Database) SpaceDiscordLinkFor(ctx context.Context, userID, spaceID string) (*SpaceDiscordLink, error) {
	items, err := db.SpaceDiscordLinksFor(ctx, userID, spaceID)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (db *Database) SpaceDiscordLinksFor(ctx context.Context, userID, spaceID string) ([]SpaceDiscordLink, error) {
	items := []SpaceDiscordLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceDiscordLinkColumns+`
			FROM space_discord_links WHERE space_id=$1 AND disabled_at IS NULL
			ORDER BY guild_name,channel_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceDiscordLink
			if err := scanSpaceDiscordLink(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// SpaceDiscordLinkByID reads a link for a service-side operation such as sync.
func (db *Database) SpaceDiscordLinkByID(ctx context.Context, spaceID, linkID string) (*SpaceDiscordLink, error) {
	out := &SpaceDiscordLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceDiscordLink(tx.QueryRowContext(ctx, `SELECT `+spaceDiscordLinkColumns+`
			FROM space_discord_links WHERE id=$1 AND space_id=$2`, linkID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SpaceDiscordLinksForChannel returns every active link watching a channel, so
// the Gateway can fan one Discord message out to each mirroring Space.
func (db *Database) SpaceDiscordLinksForChannel(ctx context.Context, guildID, channelID string) ([]SpaceDiscordLink, error) {
	items := []SpaceDiscordLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceDiscordLinkColumns+`
			FROM space_discord_links WHERE guild_id=$1 AND channel_id=$2 AND disabled_at IS NULL
			AND direction IN ('two_way','inbound')`, guildID, channelID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceDiscordLink
			if err := scanSpaceDiscordLink(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// CreateSpaceDiscordLink binds a channel. Requires integrations.manage, because
// creating a link is what authorizes Misty to write into a Discord server.
func (db *Database) CreateSpaceDiscordLink(ctx context.Context, userID string, item SpaceDiscordLink) (*SpaceDiscordLink, error) {
	item.GuildID, item.ChannelID = strings.TrimSpace(item.GuildID), strings.TrimSpace(item.ChannelID)
	if item.GuildID == "" || item.ChannelID == "" {
		return nil, ErrSpaceInvalid
	}
	if item.Direction == "" {
		item.Direction = "two_way"
	}
	if item.Direction != "two_way" && item.Direction != "inbound" && item.Direction != "outbound" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceDiscordLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var connectedByUserID string
		if err := tx.QueryRowContext(ctx, `SELECT connected_by_user_id FROM space_integrations
			WHERE id=$1 AND space_id=$2 AND provider='discord' AND status='active'`,
			item.IntegrationID, item.SpaceID).Scan(&connectedByUserID); err != nil {
			return ErrSpaceInvalid
		}
		if item.ConversationID != "" {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, item.SpaceID, item.ConversationID); err != nil {
				return err
			}
		}
		conversationID, err := ensureDiscordConversationTx(ctx, tx, userID, item)
		if err != nil {
			return err
		}
		item.ConversationID = conversationID
		return scanSpaceDiscordLink(tx.QueryRowContext(ctx, `INSERT INTO space_discord_links
			(id,space_id,integration_id,conversation_id,connected_by_user_id,guild_id,guild_name,channel_id,channel_name,direction,status,bot_user_id,webhook_id,webhook_token_ciphertext,webhook_token_nonce)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,'pending',$11,$12,$13,$14)
			ON CONFLICT(space_id,channel_id) DO UPDATE SET
				integration_id=EXCLUDED.integration_id,
				conversation_id=EXCLUDED.conversation_id,
				connected_by_user_id=EXCLUDED.connected_by_user_id,
				guild_id=EXCLUDED.guild_id,
				guild_name=EXCLUDED.guild_name,
				channel_name=EXCLUDED.channel_name,
				direction=EXCLUDED.direction,
				status='pending',
				last_error_code='',
				disabled_at=NULL,
				updated_at=NOW()
			RETURNING `+spaceDiscordLinkColumns,
			"discordlink_"+uuid.NewString(), item.SpaceID, item.IntegrationID, item.ConversationID, connectedByUserID,
			item.GuildID, item.GuildName, item.ChannelID, item.ChannelName, item.Direction,
			item.BotUserID, item.WebhookID, item.WebhookCiphertext, item.WebhookNonce), out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func ensureDiscordConversationTx(ctx context.Context, tx *sql.Tx, userID string, item SpaceDiscordLink) (string, error) {
	title := discordConversationTitle(item.ChannelName)
	if item.ConversationID != "" {
		_, err := tx.ExecContext(ctx, `UPDATE space_conversations SET
			title=$1,origin='discord',integration_id=$2,external_resource_id=$3,
			external_display_name=$4,integration_status='active',visible_to_space=TRUE,updated_at=NOW()
			WHERE id=$5 AND space_id=$6`, title, item.IntegrationID, item.ChannelID,
			item.ChannelName, item.ConversationID, item.SpaceID)
		return item.ConversationID, err
	}
	var conversationID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM space_conversations
		WHERE space_id=$1 AND origin='discord' AND external_resource_id=$2
		FOR UPDATE`, item.SpaceID, item.ChannelID).Scan(&conversationID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE space_conversations SET title=$1,integration_id=$2,
			external_display_name=$3,integration_status='active',visible_to_space=TRUE,updated_at=NOW()
			WHERE id=$4`, title, item.IntegrationID, item.ChannelName, conversationID)
		return conversationID, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	conversationID = "space_conversation_" + uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO space_conversations
		(id,space_id,title,created_by_user_id,origin,integration_id,external_resource_id,
		 external_display_name,integration_status,visible_to_space)
		VALUES($1,$2,$3,$4,'discord',$5,$6,$7,'active',TRUE)`,
		conversationID, item.SpaceID, title, userID, item.IntegrationID, item.ChannelID, item.ChannelName)
	return conversationID, err
}

func discordConversationTitle(displayName string) string {
	title := strings.TrimSpace(displayName)
	if marker := strings.LastIndex(title, "/ #"); marker >= 0 {
		title = title[marker+3:]
	}
	title = strings.TrimPrefix(title, "#")
	runes := []rune(strings.TrimSpace(title))
	if len(runes) > 80 {
		runes = runes[:80]
	}
	if len(runes) == 0 {
		return "Discord"
	}
	return string(runes)
}
