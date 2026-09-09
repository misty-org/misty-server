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
	switch {
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
			var member bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
				SELECT 1 FROM space_conversation_members cm
				JOIN space_conversations c ON c.id=cm.conversation_id
				WHERE cm.conversation_id=$1 AND cm.user_id=$2 AND c.space_id=$3
			)`, payload.ConversationID, userID, event.SpaceID).Scan(&member); err != nil {
				return false, err
			}
			if !member {
				return false, nil
			}
		}
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "node."):
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "library."):
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, event.SpaceID, "space_library_items", event.EntityID)
		if err != nil || !visible {
			return false, err
		}
		permission = PermissionLibraryView
	case strings.HasPrefix(event.EventType, "task."):
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, event.SpaceID, "space_tasks", event.EntityID)
		if err != nil || !visible {
			return false, err
		}
		permission = PermissionTasksView
	case strings.HasPrefix(event.EventType, "roadmap."):
		roadmapID := event.EntityID
		var payload struct {
			RoadmapID string `json:"roadmap_id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.RoadmapID != "" {
			roadmapID = payload.RoadmapID
		}
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, event.SpaceID, "space_roadmaps", roadmapID)
		if err != nil || !visible {
			return false, err
		}
		permission = PermissionTasksView
	case strings.HasPrefix(event.EventType, "calendar."):
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, event.SpaceID, "space_native_calendar_events", event.EntityID)
		if err != nil || !visible {
			return false, err
		}
		permission = PermissionTasksView
	case strings.HasPrefix(event.EventType, "drawing."):
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, event.SpaceID, "space_drawings", event.EntityID)
		if err != nil || !visible {
			return false, err
		}
		permission = PermissionMessagesRead
	case strings.HasPrefix(event.EventType, "agent.run."), strings.HasPrefix(event.EventType, "workflow.run."):
		run := &SpaceRun{}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE id=$1`, event.EntityID), run); errors.Is(err, sql.ErrNoRows) {
			// Retain requester-only visibility for legacy run events that predate
			// canonical space_runs rows. Never expose another member's orphaned
			// event through Studio visibility alone.
			if event.ActorUserID != userID {
				return false, nil
			}
			permission = PermissionAskRun
		} else if err != nil {
			return false, err
		} else {
			visible, err := sharedSpaceRunVisibleToUserTx(ctx, tx, run, userID)
			if err != nil || !visible {
				return false, err
			}
			if run.RequestingMemberID == userID {
				permission = PermissionAskRun
			} else {
				permission = PermissionStudioView
			}
		}
	case strings.HasPrefix(event.EventType, "note."):
		// Notes are private to their creator and explicit grantees, so a note
		// event must never reach the whole Space the way other event families
		// do. There is no Space permission that grants note visibility: the
		// answer comes from the note's own ACL, evaluated now rather than when
		// the event was recorded.
		return noteEventVisibleToUserTx(ctx, tx, userID, event)
	case strings.HasPrefix(event.EventType, "agent."), strings.HasPrefix(event.EventType, "workflow."):
		permission = PermissionStudioView
	default:
		return true, nil
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

func humanConversationParticipantTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) (bool, error) {
	var visible bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.actor_kind='person' AND cm.user_id=$2 AND c.space_id=$3)`, conversationID, userID, spaceID).Scan(&visible)
	return visible, err
}

func resourceEntityAudienceVisibleTx(ctx context.Context, tx *sql.Tx, userID, spaceID, table, entityID string) (bool, error) {
	// Table names are an internal closed set; values never come from requests.
	switch table {
	case "space_tasks", "space_roadmaps", "space_native_calendar_events", "space_library_items", "space_drawings":
	default:
		return false, ErrSpaceInvalid
	}
	var audienceKind, conversationID string
	err := tx.QueryRowContext(ctx, `SELECT audience_kind,COALESCE(audience_conversation_id,'') FROM `+table+` WHERE id=$1 AND space_id=$2`, entityID, spaceID).Scan(&audienceKind, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		// Some event families use upload/source IDs, and connected-calendar rows
		// do not live in the native table. They are Space-wide by definition.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if audienceKind == SpaceAudienceSpace {
		return true, nil
	}
	return humanConversationParticipantTx(ctx, tx, userID, spaceID, conversationID)
}

func (db *Database) CreateResolveTicket(ctx context.Context, userID, spaceID, nodeID, disposition, tokenHash string, expires time.Time) error {
	if disposition != "open" && disposition != "download" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE space_resolve_tickets SET consumed_at=NOW() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=space_resolve_tickets.space_id AND m.user_id=space_resolve_tickets.user_id)
			RETURNING user_id,space_id,node_id`, tokenHash).Scan(&userID, &spaceID, &nodeID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrSpaceForbidden
	}
	return userID, spaceID, nodeID, err
}

func (db *Database) SpaceStudioResources(ctx context.Context, userID, spaceID, kind string) ([]SpaceStudioResource, error) {
	items := []SpaceStudioResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioView); err != nil {
			return err
		}
		if kind != "workflow" {
			return ErrSpaceInvalid
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,creator_user_id,name,description,definition,enabled,version,schedules_enabled,stable_identifier,created_at,updated_at FROM space_workflows WHERE space_id=$1 ORDER BY updated_at DESC`, spaceID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceStudioResource
			item.Kind = "workflow"
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.CreatorUserID, &item.Name, &item.Description, &item.Definition, &item.Enabled, &item.Version, &item.SchedulesEnabled, &item.StableIdentifier, &item.CreatedAt, &item.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for index := range items {
			workflow, workflowErr := loadLatestWorkflowVersionTx(ctx, tx, items[index].ID)
			if workflowErr == nil {
				items[index].ActiveWorkflow = workflow
			} else if !errors.Is(workflowErr, sql.ErrNoRows) {
				return workflowErr
			}
		}
		return nil
	})
	return items, err
}
