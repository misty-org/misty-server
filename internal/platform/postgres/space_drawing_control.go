package db

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
)

// DrawingControlCommand is one retryable instruction for a DrawingRoom.
type DrawingControlCommand struct {
	ID        string
	DrawingID string
	Command   string
	Payload   []byte
	Attempts  int
}

func enqueueDrawingControlTx(
	ctx context.Context,
	tx *sql.Tx,
	drawingID, command string,
	payload map[string]any,
) error {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO space_drawing_control_outbox(
		     id,drawing_id,command,payload
		 ) VALUES($1,$2,$3,$4)`,
		"drawingctl_"+uuid.NewString(),
		drawingID,
		command,
		raw,
	)
	return err
}

// revokeDrawingAccessForSpaceTx invalidates every outstanding ticket and
// socket for a Space's drawings. Current members reconnect with fresh tickets;
// a removed member cannot mint one.
func revokeDrawingAccessForSpaceTx(
	ctx context.Context,
	tx *sql.Tx,
	spaceID string,
) error {
	rows, err := tx.QueryContext(
		ctx,
		`UPDATE space_drawings
		 SET acl_version=acl_version+1,updated_at=NOW()
		 WHERE space_id=$1 AND lifecycle_state='active'
		 RETURNING id,acl_version`,
		spaceID,
	)
	if err != nil {
		return err
	}
	type revision struct {
		drawingID  string
		aclVersion int64
	}
	revisions := []revision{}
	for rows.Next() {
		var item revision
		if err := rows.Scan(&item.drawingID, &item.aclVersion); err != nil {
			rows.Close()
			return err
		}
		revisions = append(revisions, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range revisions {
		if err := enqueueDrawingControlTx(
			ctx,
			tx,
			item.drawingID,
			"acl",
			map[string]any{"acl_version": item.aclVersion},
		); err != nil {
			return err
		}
	}
	return nil
}

// PendingDrawingControlCommands claims a bounded batch and moves each retry
// deadline forward so concurrent workers cannot deliver the same row.
func (db *Database) PendingDrawingControlCommands(
	ctx context.Context,
	limit int,
) ([]DrawingControlCommand, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	commands := []DrawingControlCommand{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`UPDATE space_drawing_control_outbox
			 SET attempts=attempts+1,
			     next_attempt_at=NOW()+(LEAST(attempts+1,6)*INTERVAL '10 seconds')
			 WHERE id IN (
			     SELECT id FROM space_drawing_control_outbox
			     WHERE delivered_at IS NULL AND next_attempt_at<=NOW()
			     ORDER BY next_attempt_at
			     FOR UPDATE SKIP LOCKED LIMIT $1)
			 RETURNING id,drawing_id,command,payload,attempts`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var command DrawingControlCommand
			if err := rows.Scan(
				&command.ID, &command.DrawingID, &command.Command,
				&command.Payload, &command.Attempts,
			); err != nil {
				return err
			}
			commands = append(commands, command)
		}
		return rows.Err()
	})
	return commands, err
}

func (db *Database) MarkDrawingControlDelivered(
	ctx context.Context,
	commandID string,
) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_drawing_control_outbox
			 SET delivered_at=NOW(),last_error=''
			 WHERE id=$1 AND delivered_at IS NULL`, commandID)
		return err
	})
}

func (db *Database) MarkDrawingControlFailed(
	ctx context.Context,
	commandID, reason string,
) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_drawing_control_outbox SET last_error=$1
			 WHERE id=$2 AND delivered_at IS NULL`,
			reason, commandID)
		return err
	})
}

// PurgeDeletedDrawings removes metadata only after the collaboration room has
// confirmed that its persisted scene is gone.
func (db *Database) PurgeDeletedDrawings(
	ctx context.Context,
	limit int,
) (int64, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var purged int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`DELETE FROM space_drawings WHERE id IN (
			     SELECT d.id FROM space_drawings d
			     WHERE d.lifecycle_state='deleting'
			       AND EXISTS (
			           SELECT 1 FROM space_drawing_control_outbox o
			           WHERE o.drawing_id=d.id
			             AND o.command='purge'
			             AND o.delivered_at IS NOT NULL)
			     LIMIT $1)`, limit)
		if err != nil {
			return err
		}
		purged, _ = result.RowsAffected()
		return nil
	})
	return purged, err
}
