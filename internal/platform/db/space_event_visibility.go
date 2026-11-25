package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func spaceEventVisibleToUserTx(ctx context.Context, tx *sql.Tx, userID string, event SpaceEvent, permissionCache map[string]bool) (bool, error) {
	permission := ""
	itemKind, isItemEvent := spaceItemKindForEvent(event.EventType)
	switch {
	case strings.HasPrefix(event.EventType, "read."):
		return event.ActorUserID != "" && event.ActorUserID == userID, nil
	case event.EventType == "conversation.removed":
		var payload struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false, err
		}
		return payload.UserID == userID, nil
	case event.EventType == "item.visibility_changed":
		var payload struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false, err
		}
		return itemAudienceReadableTx(ctx, tx, userID, event.SpaceID, payload.Kind, event.EntityID)
	case strings.HasPrefix(event.EventType, "message."), strings.HasPrefix(event.EventType, "conversation."):
		var payload struct {
			ConversationID     string   `json:"conversation_id"`
			ParticipantUserIDs []string `json:"participant_user_ids"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false, err
		}
		if payload.ConversationID == "" && strings.HasPrefix(event.EventType, "conversation.") {
			payload.ConversationID = event.EntityID
		}
		if event.EventType == "conversation.deleted" {
			visible := false
			for _, participantUserID := range payload.ParticipantUserIDs {
				if participantUserID == userID {
					visible = true
					break
				}
			}
			if !visible {
				return false, nil
			}
		} else if payload.ConversationID != "" {
			return conversationEventVisibleTx(ctx, tx, userID, event.SpaceID, payload.ConversationID, permissionCache)
		}
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "node."), strings.HasPrefix(event.EventType, "channel_category."):
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "direct."):
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "library."):
		return event.ActorUserID != "" && event.ActorUserID == userID, nil
	case isItemEvent && itemKind.Name == SpaceItemNote:
		return noteEventVisibleToUserTx(ctx, tx, userID, event)
	case isItemEvent:
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, event.SpaceID, itemKind.Table, event.EntityID)
		if err != nil || !visible {
			return false, err
		}
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "agent."):
		var payload struct {
			OwnerUserID string `json:"owner_user_id"`
			Visibility  string `json:"visibility"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false, err
		}
		if payload.Visibility == VisibilityPrivate && payload.OwnerUserID != userID {
			return false, nil
		}
		permission = PermissionStudioView
	case strings.HasPrefix(event.EventType, "agent_thread."):
		permission = PermissionStudioView
	case spaceWideEvent(event.EventType):
		return true, nil
	default:
		return false, nil
	}
	key := event.SpaceID + "\x00" + permission
	if allowed, ok := permissionCache[key]; ok {
		return allowed, nil
	}
	allowed, err := hasSpacePermissionTx(ctx, tx, userID, event.SpaceID, permission)
	if err != nil {
		return false, err
	}
	permissionCache[key] = allowed
	return allowed, nil
}

func conversationEventVisibleTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string, cache map[string]bool) (bool, error) {
	key := spaceID + "\x00conversation\x00" + conversationID
	if visible, ok := cache[key]; ok {
		return visible, nil
	}
	visible, err := conversationReadableTx(ctx, tx, userID, spaceID, conversationID)
	if err != nil {
		return false, err
	}
	cache[key] = visible
	return visible, nil
}

func conversationReadableTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) (bool, error) {
	participant, err := humanConversationParticipantTx(ctx, tx, userID, spaceID, conversationID)
	if err != nil || !participant {
		return false, err
	}
	permissions, _, err := channelPermissionsTx(ctx, tx, userID, spaceID, conversationID)
	switch {
	case errors.Is(err, ErrSpaceForbidden), errors.Is(err, ErrSpaceNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	return permissions.Has(PermissionMessagesRead), nil
}

func humanConversationParticipantTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) (bool, error) {
	var visible bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.user_id=$2 AND c.space_id=$3)`, conversationID, userID, spaceID).Scan(&visible)
	return visible, err
}

func resourceEntityAudienceVisibleTx(ctx context.Context, tx *sql.Tx, userID, spaceID, table, entityID string) (bool, error) {
	if _, made := spaceItemKindForTable(table); !made {
		return false, ErrSpaceInvalid
	}
	owner := "creator_user_id"
	var audienceKind, conversationID string
	var private bool
	err := tx.QueryRowContext(ctx, `SELECT audience_kind,COALESCE(audience_conversation_id,''),visibility='private' AND `+owner+`<>$3 FROM `+table+` WHERE id=$1 AND space_id=$2`, entityID, spaceID, userID).Scan(&audienceKind, &conversationID, &private)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if private {
		return false, nil
	}
	if audienceKind == SpaceAudienceSpace {
		return true, nil
	}
	return conversationReadableTx(ctx, tx, userID, spaceID, conversationID)
}

func (db *Database) CreateResolveTicket(ctx context.Context, userID, spaceID, nodeID, disposition, tokenHash string, expires time.Time) error {
	if disposition != "open" && disposition != "download" {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		var ok bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_nodes WHERE id=$1 AND space_id=$2 AND kind='link')`, nodeID, spaceID).Scan(&ok); err != nil || !ok {
			return ErrSpaceNotFound
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_resolve_tickets(token_hash,user_id,space_id,node_id,disposition,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, tokenHash, userID, spaceID, nodeID, disposition, expires)
		return err
	})
}

func (db *Database) ConsumeResolveTicket(ctx context.Context, tokenHash string) (string, string, string, error) {
	var userID, spaceID, nodeID string
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE space_resolve_tickets SET consumed_at=NOW() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=space_resolve_tickets.space_id AND m.user_id=space_resolve_tickets.user_id)
			RETURNING user_id,space_id,node_id`, tokenHash).Scan(&userID, &spaceID, &nodeID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrSpaceForbidden
	}
	return userID, spaceID, nodeID, err
}

var spaceWideEventFamilies = []string{"space.", "member.", "owner.", "role.", "roles.", "drive."}

func spaceWideEvent(eventType string) bool {
	for _, family := range spaceWideEventFamilies {
		if strings.HasPrefix(eventType, family) {
			return true
		}
	}
	return false
}
