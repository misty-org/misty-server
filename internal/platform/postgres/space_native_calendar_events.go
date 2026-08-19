package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const nativeCalendarEventColumns = `id,space_id,title,description,location,starts_at,ends_at,all_day,timezone,status,audience_kind,COALESCE(audience_conversation_id,''),created_by_user_id,COALESCE(created_by_agent_id,''),COALESCE(source_run_id,''),version,archived_at,created_at,updated_at`

func scanNativeCalendarEvent(scanner interface{ Scan(...any) error }, out *SpaceCalendarEvent) error {
	var archivedAt *time.Time
	out.Provider, out.SourceID, out.Origin = "misty", "misty", "native"
	return scanner.Scan(&out.ID, &out.SpaceID, &out.Title, &out.Description, &out.Location,
		&out.StartsAt, &out.EndsAt, &out.AllDay, &out.Timezone, &out.Status,
		&out.AudienceKind, &out.AudienceConversationID, &out.CreatedByUserID,
		&out.CreatedByAgentID, &out.SourceRunID, &out.Version, &archivedAt,
		&out.CreatedAt, &out.UpdatedAt)
}

func validateNativeCalendarEvent(item *SpaceCalendarEvent) error {
	item.Title = strings.TrimSpace(item.Title)
	item.Description = strings.TrimSpace(item.Description)
	item.Location = strings.TrimSpace(item.Location)
	item.Timezone = strings.TrimSpace(item.Timezone)
	if item.Timezone == "" {
		item.Timezone = "UTC"
	}
	if item.Status == "" {
		item.Status = "confirmed"
	}
	if item.Title == "" || len([]rune(item.Title)) > 240 || len([]rune(item.Description)) > 20000 || len([]rune(item.Location)) > 1000 || item.StartsAt.IsZero() || item.EndsAt.Before(item.StartsAt) {
		return ErrSpaceInvalid
	}
	if _, err := time.LoadLocation(item.Timezone); err != nil {
		return ErrSpaceInvalid
	}
	switch item.Status {
	case "confirmed", "tentative", "canceled":
		return nil
	default:
		return ErrSpaceInvalid
	}
}

func (db *Database) CreateNativeCalendarEvent(ctx context.Context, userID string, item SpaceCalendarEvent) (*SpaceCalendarEvent, error) {
	if item.ID == "" {
		item.ID = "native_event_" + uuid.NewString()
	}
	if item.CreatedByUserID == "" {
		item.CreatedByUserID = userID
	}
	if err := validateNativeCalendarEvent(&item); err != nil {
		return nil, err
	}
	audience, err := NormalizeResourceAudience(item.AudienceKind, item.AudienceConversationID)
	if err != nil {
		return nil, err
	}
	out := &SpaceCalendarEvent{}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionTasksManage); err != nil {
			return err
		}
		if err := validateResourceAudienceTx(ctx, tx, userID, item.SpaceID, audience); err != nil {
			return err
		}
		query := `INSERT INTO space_native_calendar_events(id,space_id,title,description,location,starts_at,ends_at,all_day,timezone,status,audience_kind,audience_conversation_id,created_by_user_id,created_by_agent_id,source_run_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),$13,NULLIF($14,''),NULLIF($15,'')) RETURNING ` + nativeCalendarEventColumns
		if err := scanNativeCalendarEvent(tx.QueryRowContext(ctx, query, item.ID, item.SpaceID, item.Title, item.Description, item.Location, item.StartsAt, item.EndsAt, item.AllDay, item.Timezone, item.Status, audience.Kind, audience.ConversationID, item.CreatedByUserID, item.CreatedByAgentID, item.SourceRunID), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, item.SpaceID, userID, "calendar.event.created", item.ID, map[string]any{"event": out})
		return err
	})
	return out, err
}

func (db *Database) UpdateNativeCalendarEvent(ctx context.Context, userID string, item SpaceCalendarEvent) (*SpaceCalendarEvent, error) {
	if item.ID == "" || item.SpaceID == "" || item.Version < 1 || validateNativeCalendarEvent(&item) != nil {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceCalendarEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionTasksManage); err != nil {
			return err
		}
		err := scanNativeCalendarEvent(tx.QueryRowContext(ctx, `UPDATE space_native_calendar_events SET title=$1,description=$2,location=$3,starts_at=$4,ends_at=$5,all_day=$6,timezone=$7,status=$8,version=version+1,updated_at=NOW() WHERE id=$9 AND space_id=$10 AND version=$11 AND archived_at IS NULL RETURNING `+nativeCalendarEventColumns, item.Title, item.Description, item.Location, item.StartsAt, item.EndsAt, item.AllDay, item.Timezone, item.Status, item.ID, item.SpaceID, item.Version), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceConflict
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, item.SpaceID, userID, "calendar.event.updated", item.ID, map[string]any{"event": out})
		return err
	})
	return out, err
}

func (db *Database) NativeCalendarEvent(ctx context.Context, userID, spaceID, eventID string) (*SpaceCalendarEvent, error) {
	out := &SpaceCalendarEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		err := scanNativeCalendarEvent(tx.QueryRowContext(ctx, `SELECT `+nativeCalendarEventColumns+` FROM space_native_calendar_events WHERE id=$1 AND space_id=$2 AND archived_at IS NULL AND (audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$3))`, eventID, spaceID, userID), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	return out, err
}

func (db *Database) ArchiveNativeCalendarEvent(ctx context.Context, userID, spaceID, eventID string, version int64) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_native_calendar_events SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND version=$3 AND archived_at IS NULL`, eventID, spaceID, version)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceConflict
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "calendar.event.archived", eventID, nil)
		return err
	})
}

func appendNativeCalendarEventsTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, from, to time.Time, out []SpaceCalendarEvent) ([]SpaceCalendarEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+nativeCalendarEventColumns+` FROM space_native_calendar_events WHERE space_id=$1 AND starts_at<$2 AND ends_at>$3 AND archived_at IS NULL AND (audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4)) ORDER BY starts_at,id`, spaceID, to, from, userID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SpaceCalendarEvent
		if err := scanNativeCalendarEvent(rows, &item); err != nil {
			return out, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
