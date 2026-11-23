package db

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
)

type CodeSessionControlCommand struct {
	ID        string
	SessionID string
	Command   string
	Payload   []byte
	Attempts  int
}

func enqueueCodeSessionControlTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID, command string,
	payload map[string]any,
) error {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO space_code_session_control_outbox(id,session_id,command,payload)
		 VALUES($1,$2,$3,$4)`,
		"codectl_"+uuid.NewString(), sessionID, command, raw)
	return err
}

func (db *Database) PendingCodeSessionControlCommands(
	ctx context.Context,
	limit int,
) ([]CodeSessionControlCommand, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	commands := []CodeSessionControlCommand{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`UPDATE space_code_session_control_outbox
			 SET attempts=attempts+1,
			     next_attempt_at=NOW()+(LEAST(attempts+1,6)*INTERVAL '10 seconds')
			 WHERE id IN (
			     SELECT id FROM space_code_session_control_outbox
			     WHERE delivered_at IS NULL AND next_attempt_at<=NOW()
			     ORDER BY next_attempt_at, created_at
			     FOR UPDATE SKIP LOCKED LIMIT $1)
			 RETURNING id,session_id,command,payload,attempts`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var command CodeSessionControlCommand
			if err := rows.Scan(
				&command.ID, &command.SessionID, &command.Command,
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

func (db *Database) MarkCodeSessionControlDelivered(ctx context.Context, commandID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_code_session_control_outbox
			 SET delivered_at=NOW(),last_error=''
			 WHERE id=$1 AND delivered_at IS NULL`, commandID)
		return err
	})
}

func (db *Database) AbandonCodeSessionControlCommand(ctx context.Context, commandID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_code_session_control_outbox
			 SET delivered_at=NOW(),last_error=$1
			 WHERE id=$2 AND delivered_at IS NULL`, reason, commandID)
		return err
	})
}

func (db *Database) MarkCodeSessionControlFailed(ctx context.Context, commandID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_code_session_control_outbox SET last_error=$1
			 WHERE id=$2 AND delivered_at IS NULL`, reason, commandID)
		return err
	})
}
