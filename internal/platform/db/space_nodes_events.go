package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (db *Database) DeleteSpaceNode(ctx context.Context, userID, spaceID, nodeID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "node.removed", nodeID, map[string]any{}); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_nodes WHERE id=$1 AND space_id=$2 AND (visibility='public' OR uploader_user_id=$3)`, nodeID, spaceID, userID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) MarkSpaceRead(ctx context.Context, userID, spaceID string, seq int64) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		var previous, next int64
		if err := tx.QueryRowContext(ctx, `SELECT read_message_seq FROM space_members WHERE space_id=$1 AND user_id=$2 FOR NO KEY UPDATE`, spaceID, userID).Scan(&previous); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE space_members SET read_message_seq=GREATEST(read_message_seq,
			LEAST($1,(SELECT COALESCE(MAX(seq),0) FROM space_messages WHERE space_id=$2 AND conversation_id IS NULL)))
			WHERE space_id=$2 AND user_id=$3 RETURNING read_message_seq`, seq, spaceID, userID).Scan(&next); err != nil {
			return err
		}
		return recordReadMarkerTx(ctx, tx, spaceID, userID, "", "", previous, next)
	})
}

func recordReadMarkerTx(ctx context.Context, tx *sql.Tx, spaceID, userID, conversationID, threadRootID string, previous, next int64) error {
	if next <= previous {
		return nil
	}
	payload := map[string]any{"user_id": userID, "conversation_id": conversationID, "seq": next}
	if threadRootID != "" {
		payload["thread_root_id"] = threadRootID
	}
	_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "read.updated", "", payload)
	return err
}

func (db *Database) CreateRealtimeTicket(ctx context.Context, userID, sessionID, tokenHash string, after int64, expires time.Time) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO realtime_tickets(token_hash,user_id,session_id,after_cursor,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenHash, userID, sessionID, after, expires)
		return err
	})
}

func (db *Database) ConsumeRealtimeTicket(ctx context.Context, tokenHash string) (string, string, int64, error) {
	var userID, sessionID string
	var after int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE realtime_tickets SET consumed_at=NOW() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>NOW() RETURNING user_id,session_id,after_cursor`, tokenHash).Scan(&userID, &sessionID, &after)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, ErrSpaceForbidden
	}
	return userID, sessionID, after, err
}

type SpaceReplay struct {
	Events []SpaceEvent
	Recent []SpaceEvent
	Resync bool
	Newest int64
}

const (
	spaceReplayOverlapLimit  = 200
	spaceReplayOverlapWindow = "2 minutes"
)

func (db *Database) SpaceEventsAfter(ctx context.Context, userID string, after int64, limit int) ([]SpaceEvent, bool, error) {
	replay, err := db.SpaceEventsReplay(ctx, userID, after, limit)
	return replay.Events, replay.Resync, err
}

func (db *Database) SpaceEventsReplay(ctx context.Context, userID string, after int64, limit int) (SpaceReplay, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	events := []SpaceEvent{}
	recent := []SpaceEvent{}
	resync := false
	var newest int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var issued int64
		var pruned, purged bool
		if err := tx.QueryRowContext(ctx, `SELECT
				GREATEST(COALESCE((SELECT max(id) FROM space_events),0),COALESCE((SELECT max(purged_through) FROM space_event_horizon),0)),
				(SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM space_events_id_seq),
				COALESCE((SELECT e.created_at<=NOW()-INTERVAL '7 days' FROM space_events e JOIN space_members m ON m.space_id=e.space_id
					WHERE m.user_id=$2 AND e.id>$1 ORDER BY e.id LIMIT 1),false),
				EXISTS(SELECT 1 FROM space_event_horizon h JOIN space_members m ON m.space_id=h.space_id
					WHERE m.user_id=$2 AND h.purged_through>$1)`, after, userID).Scan(&newest, &issued, &pruned, &purged); err != nil {
			return err
		}
		if after > 0 {
			resync = pruned || purged || after > max(newest, issued)
		}
		permissionCache := map[string]bool{}
		if after > 0 {
			overlap, err := spaceEventsJustBeforeTx(ctx, tx, userID, after, permissionCache)
			if err != nil {
				return err
			}
			recent = overlap
		}
		cursor := after
		const batchSize = 500
		for len(events) < limit {
			rows, err := tx.QueryContext(ctx, `SELECT e.id,e.space_id,e.event_type,COALESCE(e.actor_user_id,''),COALESCE(e.entity_id,''),e.payload,e.created_at
				FROM space_events e JOIN space_members m ON m.space_id=e.space_id
				WHERE m.user_id=$1 AND e.id>$2 AND e.created_at>NOW()-INTERVAL '7 days'
				ORDER BY e.id LIMIT $3`, userID, cursor, batchSize)
			if err != nil {
				return err
			}
			batch := make([]SpaceEvent, 0, batchSize)
			for rows.Next() {
				var event SpaceEvent
				if err := rows.Scan(&event.ID, &event.SpaceID, &event.EventType, &event.ActorUserID, &event.EntityID, &event.Payload, &event.CreatedAt); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, event)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
			for _, event := range batch {
				cursor = event.ID
				visible, err := spaceEventVisibleToUserTx(ctx, tx, userID, event, permissionCache)
				if err != nil {
					return err
				}
				if visible {
					events = append(events, event)
					if len(events) == limit {
						break
					}
				}
			}
			if len(batch) < batchSize {
				break
			}
		}
		return nil
	})
	return SpaceReplay{Events: events, Recent: recent, Resync: resync, Newest: newest}, err
}

func spaceEventsJustBeforeTx(ctx context.Context, tx *sql.Tx, userID string, through int64, permissionCache map[string]bool) ([]SpaceEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.space_id,e.event_type,COALESCE(e.actor_user_id,''),COALESCE(e.entity_id,''),e.payload,e.created_at
		FROM space_events e JOIN space_members m ON m.space_id=e.space_id
		WHERE m.user_id=$1 AND e.id<=$2 AND e.created_at>NOW()-INTERVAL '`+spaceReplayOverlapWindow+`'
			AND ((e.event_type NOT LIKE 'read.%' AND e.event_type NOT LIKE 'library.%') OR e.actor_user_id=$1)
		ORDER BY e.id DESC LIMIT $3`, userID, through, spaceReplayOverlapLimit)
	if err != nil {
		return nil, err
	}
	batch := []SpaceEvent{}
	for rows.Next() {
		var event SpaceEvent
		if err := rows.Scan(&event.ID, &event.SpaceID, &event.EventType, &event.ActorUserID, &event.EntityID, &event.Payload, &event.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		batch = append(batch, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]SpaceEvent, 0, len(batch))
	for index := len(batch) - 1; index >= 0; index-- {
		visible, err := spaceEventVisibleToUserTx(ctx, tx, userID, batch[index], permissionCache)
		if err != nil {
			return nil, err
		}
		if visible {
			out = append(out, batch[index])
		}
	}
	return out, nil
}

func (db *Database) EventByIDForUser(ctx context.Context, userID string, eventID int64) (*SpaceEvent, error) {
	out := &SpaceEvent{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT e.id,e.space_id,e.event_type,COALESCE(e.actor_user_id,''),COALESCE(e.entity_id,''),e.payload,e.created_at
			FROM space_events e JOIN space_members m ON m.space_id=e.space_id
			WHERE e.id=$1 AND m.user_id=$2`, eventID, userID).Scan(&out.ID, &out.SpaceID, &out.EventType, &out.ActorUserID, &out.EntityID, &out.Payload, &out.CreatedAt); err != nil {
			return err
		}
		visible, err := spaceEventVisibleToUserTx(ctx, tx, userID, *out, map[string]bool{})
		if err != nil {
			return err
		}
		if !visible {
			return sql.ErrNoRows
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}
