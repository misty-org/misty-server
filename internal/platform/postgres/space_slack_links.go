package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SpaceSlackLink struct {
	ID                string     `json:"id"`
	SpaceID           string     `json:"space_id"`
	IntegrationID     string     `json:"integration_id"`
	SharedResourceID  string     `json:"shared_resource_id"`
	ConversationID    string     `json:"conversation_id"`
	ConnectedByUserID string     `json:"connected_by_user_id"`
	TeamID            string     `json:"team_id"`
	TeamName          string     `json:"team_name"`
	ChannelID         string     `json:"channel_id"`
	ChannelName       string     `json:"channel_name"`
	Direction         string     `json:"direction"`
	Status            string     `json:"status"`
	LastMessageTS     string     `json:"last_message_ts,omitempty"`
	LastErrorCode     string     `json:"last_error_code,omitempty"`
	BotUserID         string     `json:"bot_user_id,omitempty"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	DisabledAt        *time.Time `json:"disabled_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const spaceSlackLinkColumns = `id,space_id,integration_id,shared_resource_id,conversation_id,
	connected_by_user_id,team_id,team_name,channel_id,channel_name,direction,status,
	last_message_ts,last_synced_at,last_error_code,bot_user_id,disabled_at,created_at,updated_at`

func scanSpaceSlackLink(row interface{ Scan(...any) error }, item *SpaceSlackLink) error {
	return row.Scan(&item.ID, &item.SpaceID, &item.IntegrationID, &item.SharedResourceID,
		&item.ConversationID, &item.ConnectedByUserID, &item.TeamID, &item.TeamName,
		&item.ChannelID, &item.ChannelName, &item.Direction, &item.Status,
		&item.LastMessageTS, &item.LastSyncedAt, &item.LastErrorCode, &item.BotUserID,
		&item.DisabledAt, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) SpaceSlackLinksFor(ctx context.Context, userID, spaceID string) ([]SpaceSlackLink, error) {
	items := []SpaceSlackLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceSlackLinkColumns+`
			FROM space_slack_links WHERE space_id=$1 AND disabled_at IS NULL
			ORDER BY team_name,channel_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceSlackLink
			if err := scanSpaceSlackLink(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SpaceSlackLinkByID(ctx context.Context, spaceID, linkID string) (*SpaceSlackLink, error) {
	item := &SpaceSlackLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceSlackLink(tx.QueryRowContext(ctx, `SELECT `+spaceSlackLinkColumns+`
			FROM space_slack_links WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, linkID, spaceID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) SpaceSlackLinkForResource(ctx context.Context, resourceID string) (*SpaceSlackLink, error) {
	item := &SpaceSlackLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceSlackLink(tx.QueryRowContext(ctx, `SELECT `+spaceSlackLinkColumns+`
			FROM space_slack_links WHERE shared_resource_id=$1 AND disabled_at IS NULL`, resourceID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) CreateSpaceSlackLink(ctx context.Context, userID string, item SpaceSlackLink) (*SpaceSlackLink, error) {
	item.SpaceID, item.IntegrationID = strings.TrimSpace(item.SpaceID), strings.TrimSpace(item.IntegrationID)
	item.SharedResourceID, item.ChannelID = strings.TrimSpace(item.SharedResourceID), strings.TrimSpace(item.ChannelID)
	if item.SpaceID == "" || item.IntegrationID == "" || item.SharedResourceID == "" || item.ChannelID == "" {
		return nil, ErrSpaceInvalid
	}
	if item.Direction == "" {
		item.Direction = "two_way"
	}
	if !oneOf(item.Direction, "two_way", "inbound", "outbound") {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceSlackLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var connectedBy, teamID, teamName, channelID, channelName string
		err := tx.QueryRowContext(ctx, `SELECT i.connected_by_user_id,c.account_id,c.account_display,
			r.external_resource_id,r.display_name FROM provider_shared_resources r
			JOIN space_integrations i ON i.id=r.integration_id
			JOIN space_provider_credentials c ON c.integration_id=i.id AND c.revoked_at IS NULL
			WHERE r.id=$1 AND r.space_id=$2 AND r.integration_id=$3 AND r.provider='slack'
			AND r.resource_type='channel' AND r.status='active' AND i.status='active'`,
			item.SharedResourceID, item.SpaceID, item.IntegrationID).
			Scan(&connectedBy, &teamID, &teamName, &channelID, &channelName)
		if err != nil || channelID != item.ChannelID || teamID == "" {
			return ErrSpaceInvalid
		}
		conversationID, err := ensureSlackConversationTx(ctx, tx, userID, item.SpaceID,
			item.IntegrationID, channelID, channelName, item.ConversationID)
		if err != nil {
			return err
		}
		return scanSpaceSlackLink(tx.QueryRowContext(ctx, `INSERT INTO space_slack_links
			(id,space_id,integration_id,shared_resource_id,conversation_id,connected_by_user_id,
			 team_id,team_name,channel_id,channel_name,direction,status,bot_user_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending',$12)
			ON CONFLICT(space_id,channel_id) DO UPDATE SET
			 integration_id=EXCLUDED.integration_id,shared_resource_id=EXCLUDED.shared_resource_id,
			 conversation_id=EXCLUDED.conversation_id,connected_by_user_id=EXCLUDED.connected_by_user_id,
			 team_id=EXCLUDED.team_id,team_name=EXCLUDED.team_name,channel_name=EXCLUDED.channel_name,
			 direction=EXCLUDED.direction,status='pending',last_error_code='',bot_user_id=EXCLUDED.bot_user_id,
			 disabled_at=NULL,updated_at=NOW() RETURNING `+spaceSlackLinkColumns,
			"slacklink_"+uuid.NewString(), item.SpaceID, item.IntegrationID, item.SharedResourceID,
			conversationID, connectedBy, teamID, teamName, channelID, channelName,
			item.Direction, item.BotUserID), out)
	})
	return out, err
}

func ensureSlackConversationTx(ctx context.Context, tx *sql.Tx, userID, spaceID, integrationID, channelID, channelName, conversationID string) (string, error) {
	title := providerConversationTitle(channelName, "Slack")
	if conversationID != "" {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return "", err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_conversations SET title=$1,origin='slack',
			integration_id=$2,external_resource_id=$3,external_display_name=$4,
			integration_status='active',visible_to_space=TRUE,updated_at=NOW()
			WHERE id=$5 AND space_id=$6`, title, integrationID, channelID, channelName, conversationID, spaceID)
		return conversationID, err
	}
	err := tx.QueryRowContext(ctx, `SELECT id FROM space_conversations
		WHERE space_id=$1 AND origin='slack' AND external_resource_id=$2 FOR UPDATE`,
		spaceID, channelID).Scan(&conversationID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE space_conversations SET title=$1,integration_id=$2,
			external_display_name=$3,integration_status='active',visible_to_space=TRUE,updated_at=NOW()
			WHERE id=$4`, title, integrationID, channelName, conversationID)
		return conversationID, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	conversationID = "space_conversation_" + uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO space_conversations
		(id,space_id,title,created_by_user_id,origin,integration_id,external_resource_id,
		 external_display_name,integration_status,visible_to_space)
		VALUES($1,$2,$3,$4,'slack',$5,$6,$7,'active',TRUE)`, conversationID,
		spaceID, title, userID, integrationID, channelID, channelName)
	return conversationID, err
}

func providerConversationTitle(displayName, fallback string) string {
	title := strings.TrimPrefix(strings.TrimSpace(displayName), "#")
	runes := []rune(title)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	if len(runes) == 0 {
		return fallback
	}
	return string(runes)
}
