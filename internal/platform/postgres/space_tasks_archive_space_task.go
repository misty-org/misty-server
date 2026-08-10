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

// ArchiveSpaceTask writes a tombstone. version is accepted for wire
// compatibility with existing clients but is deliberately not part of the
// predicate: archiving is last-write-wins and idempotent.
func (db *Database) ArchiveSpaceTask(ctx context.Context, actorUserID, spaceID, taskID string, _ int64) (*SpaceTask, error) {
	out := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, actorUserID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		// Archiving is idempotent and does not depend on the client's version:
		// re-archiving an already-archived task returns the existing tombstone
		// rather than conflicting.
		err := scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND archived_at IS NULL RETURNING `+spaceTaskColumns, taskID, spaceID), out)
		if errors.Is(err, sql.ErrNoRows) {
			existing := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE id=$1 AND space_id=$2`, taskID, spaceID), out)
			if errors.Is(existing, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return existing
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, actorUserID, "task.archived", taskID, map[string]any{"version": out.Version})
		return err
	})
	return out, err
}

const calendarSourceColumns = `id,space_id,integration_id,connected_by_user_id,provider,external_calendar_id,display_name,timezone,sync_token,watch_channel_id,watch_resource_id,watch_token_hash,watch_expires_at,status,last_error_code,last_reconciled_at,disabled_at,created_at,updated_at`

func scanCalendarSource(row interface{ Scan(...any) error }, out *SpaceCalendarSource) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.IntegrationID, &out.ConnectedByUserID, &out.Provider, &out.ExternalCalendarID, &out.DisplayName, &out.Timezone, &out.SyncToken, &out.WatchChannelID, &out.WatchResourceID, &out.WatchTokenHash, &out.WatchExpiresAt, &out.Status, &out.LastErrorCode, &out.LastReconciledAt, &out.DisabledAt, &out.CreatedAt, &out.UpdatedAt)
}

func (db *Database) SpaceCalendarSources(ctx context.Context, userID, spaceID string) ([]SpaceCalendarSource, error) {
	out := []SpaceCalendarSource{{ID: "misty", SpaceID: spaceID, Provider: "misty", ExternalCalendarID: "misty", DisplayName: "Misty", Timezone: "UTC", Status: "active"}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+calendarSourceColumns+` FROM space_calendar_sources WHERE space_id=$1 ORDER BY display_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceCalendarSource
			if err := scanCalendarSource(rows, &item); err != nil {
				return err
			}
			item.Provider = "google"
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) CreateSpaceCalendarSource(ctx context.Context, userID string, item SpaceCalendarSource) (*SpaceCalendarSource, error) {
	item.ID = "calendar_source_" + uuid.NewString()
	item.Provider = "google"
	item.ExternalCalendarID = strings.TrimSpace(item.ExternalCalendarID)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.Timezone = strings.TrimSpace(item.Timezone)
	if item.Timezone == "" {
		item.Timezone = "UTC"
	}
	if item.SpaceID == "" || item.IntegrationID == "" || item.ExternalCalendarID == "" || item.DisplayName == "" {
		return nil, ErrSpaceInvalid
	}
	if _, err := time.LoadLocation(item.Timezone); err != nil {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceCalendarSource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var connectedByUserID string
		if err := tx.QueryRowContext(ctx, `SELECT connected_by_user_id FROM space_integrations
			WHERE id=$1 AND space_id=$2 AND provider='google' AND status='active'`,
			item.IntegrationID, item.SpaceID).Scan(&connectedByUserID); err != nil {
			return ErrSpaceInvalid
		}
		query := `INSERT INTO space_calendar_sources(id,space_id,integration_id,connected_by_user_id,provider,external_calendar_id,display_name,timezone)
			VALUES($1,$2,$3,$4,'google',$5,$6,$7) ON CONFLICT(space_id,integration_id,external_calendar_id) DO UPDATE SET display_name=EXCLUDED.display_name,timezone=EXCLUDED.timezone,status='pending',disabled_at=NULL,updated_at=NOW() RETURNING ` + calendarSourceColumns
		if err := scanCalendarSource(tx.QueryRowContext(ctx, query, item.ID, item.SpaceID, item.IntegrationID, connectedByUserID, item.ExternalCalendarID, item.DisplayName, item.Timezone), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, item.SpaceID, userID, "calendar.source_published", out.ID, map[string]any{"source": out})
		return err
	})
	return out, err
}

func (db *Database) DisableSpaceCalendarSource(ctx context.Context, userID, spaceID, sourceID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_calendar_sources SET status='disabled',disabled_at=NOW(),sync_token='',watch_channel_id='',watch_resource_id='',watch_token_hash='',watch_expires_at=NULL,updated_at=NOW() WHERE id=$1 AND space_id=$2`, sourceID, spaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "calendar.source_disabled", sourceID, map[string]any{})
		return err
	})
}

func (db *Database) SpaceCalendarEvents(ctx context.Context, userID, spaceID string, from, to time.Time) ([]SpaceCalendarEvent, error) {
	out := []SpaceCalendarEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,source_id,provider,external_event_id,fingerprint,title,description,location,meeting_url,organizer,starts_at,ends_at,all_day,timezone,status,provider_created_at,provider_updated_at,removed_at,created_at,updated_at
			FROM space_calendar_events WHERE space_id=$1 AND starts_at<$2 AND ends_at>$3 AND removed_at IS NULL ORDER BY starts_at,id`, spaceID, to, from)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceCalendarEvent
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.SourceID, &item.Provider, &item.ExternalEventID, &item.Fingerprint, &item.Title, &item.Description, &item.Location, &item.MeetingURL, &item.Organizer, &item.StartsAt, &item.EndsAt, &item.AllDay, &item.Timezone, &item.Status, &item.ProviderCreatedAt, &item.ProviderUpdatedAt, &item.RemovedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out, err = appendNativeCalendarEventsTx(ctx, tx, userID, spaceID, from, to, out)
		return err
	})
	return out, err
}

func (db *Database) CalendarSourceByWatchChannel(ctx context.Context, channelID string) (*SpaceCalendarSource, error) {
	out := &SpaceCalendarSource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanCalendarSource(tx.QueryRowContext(ctx, `SELECT `+calendarSourceColumns+` FROM space_calendar_sources WHERE watch_channel_id=$1 AND status IN ('active','syncing')`, channelID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) UpdateCalendarSourceSync(ctx context.Context, sourceID, syncToken, channelID, resourceID, tokenHash, status, errorCode string, expiresAt *time.Time) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_calendar_sources SET sync_token=$1,watch_channel_id=COALESCE(NULLIF($2,''),watch_channel_id),watch_resource_id=COALESCE(NULLIF($3,''),watch_resource_id),watch_token_hash=COALESCE(NULLIF($4,''),watch_token_hash),watch_expires_at=COALESCE($5,watch_expires_at),status=$6,last_error_code=$7,last_reconciled_at=CASE WHEN $6='active' THEN NOW() ELSE last_reconciled_at END,updated_at=NOW() WHERE id=$8`, syncToken, channelID, resourceID, tokenHash, expiresAt, status, errorCode, sourceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) UpsertSpaceCalendarEvent(ctx context.Context, item SpaceCalendarEvent) error {
	if item.ID == "" {
		item.ID = "calendar_event_" + uuid.NewString()
	}
	if len(item.Organizer) == 0 {
		item.Organizer = json.RawMessage(`{}`)
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO space_calendar_events(id,space_id,source_id,provider,external_event_id,fingerprint,title,description,location,meeting_url,organizer,starts_at,ends_at,all_day,timezone,status,provider_created_at,provider_updated_at,removed_at)
			VALUES($1,$2,$3,'google',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT(source_id,external_event_id) DO UPDATE SET fingerprint=EXCLUDED.fingerprint,title=EXCLUDED.title,description=EXCLUDED.description,location=EXCLUDED.location,meeting_url=EXCLUDED.meeting_url,organizer=EXCLUDED.organizer,starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at,all_day=EXCLUDED.all_day,timezone=EXCLUDED.timezone,status=EXCLUDED.status,provider_created_at=EXCLUDED.provider_created_at,provider_updated_at=EXCLUDED.provider_updated_at,removed_at=EXCLUDED.removed_at,updated_at=NOW()`, item.ID, item.SpaceID, item.SourceID, item.ExternalEventID, item.Fingerprint, item.Title, item.Description, item.Location, item.MeetingURL, item.Organizer, item.StartsAt, item.EndsAt, item.AllDay, item.Timezone, item.Status, item.ProviderCreatedAt, item.ProviderUpdatedAt, item.RemovedAt)
		return err
	})
}

func (db *Database) MarkSpaceCalendarEventRemoved(ctx context.Context, sourceID, externalEventID string, removedAt time.Time) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_calendar_events SET status='canceled',removed_at=$1,updated_at=NOW() WHERE source_id=$2 AND external_event_id=$3`, removedAt, sourceID, externalEventID)
		return err
	})
}

// InvalidateSpaceCalendarEvents implements Google's 410 contract: once a sync
// token is invalid, the local projection can no longer be trusted and must be
// rebuilt by a full synchronization.
func (db *Database) InvalidateSpaceCalendarEvents(ctx context.Context, sourceID string, removedAt time.Time) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_calendar_events SET status='canceled',removed_at=$1,updated_at=NOW() WHERE source_id=$2 AND removed_at IS NULL`, removedAt, sourceID)
		return err
	})
}

func (db *Database) CalendarSourcesNeedingReconciliation(ctx context.Context, limit int) ([]SpaceCalendarSource, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	out := []SpaceCalendarSource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+calendarSourceColumns+` FROM space_calendar_sources
			WHERE status IN ('pending','active','needs_attention') AND disabled_at IS NULL AND
			(last_reconciled_at IS NULL OR last_reconciled_at<NOW()-INTERVAL '15 minutes' OR watch_expires_at IS NULL OR watch_expires_at<NOW()+INTERVAL '24 hours')
			ORDER BY COALESCE(last_reconciled_at,'epoch'),id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceCalendarSource
			if err := scanCalendarSource(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
