package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (db *Database) DeleteSpaceNode(ctx context.Context, userID, spaceID, nodeID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "node.removed", nodeID, map[string]any{}); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_nodes WHERE id=$1 AND space_id=$2`, nodeID, spaceID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) MarkSpaceNodeStale(ctx context.Context, userID, spaceID, nodeID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_nodes SET stale=TRUE,updated_at=NOW() WHERE id=$1 AND space_id=$2`, nodeID, spaceID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "node.stale", nodeID, map[string]any{})
		return err
	})
}

func (db *Database) MarkSpaceRead(ctx context.Context, userID, spaceID string, seq int64) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_members SET read_message_seq=GREATEST(read_message_seq,$1) WHERE space_id=$2 AND user_id=$3`, seq, spaceID, userID)
		return err
	})
}

func (db *Database) CreateRealtimeTicket(ctx context.Context, userID, tokenHash string, after int64, expires time.Time) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO realtime_tickets(token_hash,user_id,after_cursor,expires_at) VALUES($1,$2,$3,$4)`, tokenHash, userID, after, expires)
		return err
	})
}

func (db *Database) ConsumeRealtimeTicket(ctx context.Context, tokenHash string) (string, int64, error) {
	var userID string
	var after int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE realtime_tickets SET consumed_at=NOW() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>NOW() RETURNING user_id,after_cursor`, tokenHash).Scan(&userID, &after)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSpaceForbidden
	}
	return userID, after, err
}

func (db *Database) SpaceEventsAfter(ctx context.Context, userID string, after int64, limit int) ([]SpaceEvent, bool, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	events := []SpaceEvent{}
	resync := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if after > 0 {
			var oldest sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT min(id) FROM space_events WHERE created_at>NOW()-INTERVAL '7 days'`).Scan(&oldest); err != nil {
				return err
			}
			resync = oldest.Valid && after < oldest.Int64-1
		}
		permissionCache := map[string]bool{}
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
	return events, resync, err
}

func (db *Database) EventByIDForUser(ctx context.Context, userID string, eventID int64) (*SpaceEvent, error) {
	out := &SpaceEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
