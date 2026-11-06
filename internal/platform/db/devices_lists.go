package db

import (
	"context"
	"database/sql"
	"errors"
)

// RemoveDevice revokes a device everywhere: its key is refused forever, its
// account session ends and its queued jobs are canceled.
func (db *Database) RemoveDevice(ctx context.Context, userID, targetID string) (sessionHash string, err error) {
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, userID); err != nil {
			return err
		}
		var state, key string
		var syncDevice, vault, session sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT admission_state,public_key,sync_device_id,vault_id,session_hash FROM trusted_devices WHERE id=$1 AND user_id=$2 FOR UPDATE`, targetID, userID).
			Scan(&state, &key, &syncDevice, &vault, &session)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		if err != nil {
			return err
		}
		if state == "revoked" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_revoked_keys(user_id,public_key,device_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, key, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE trusted_devices SET admission_state='revoked',revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW() WHERE id=$1 AND user_id=$2`, targetID, userID); err != nil {
			return err
		}
		if session.Valid && session.String != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=$1 AND user_id=$2`, session.String, userID); err != nil {
				return err
			}
			sessionHash = session.String
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workflow_device_node_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,NOW())
			WHERE user_id=$1 AND assigned_device_id=$2 AND state IN ('queued','leased','running')`, userID, targetID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM device_channel_tickets WHERE device_id=$1`, targetID)
		return err
	})
	return sessionHash, err
}

// RenameDevice sets the one name every surface shows for a device, including
// its sync workspace.
func (db *Database) RenameDevice(ctx context.Context, userID, targetID, name string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var syncDevice, vault sql.NullString
		err := tx.QueryRowContext(ctx, `UPDATE trusted_devices SET name=$3,name_updated_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND identity_version=2 AND admission_state<>'revoked' RETURNING sync_device_id,vault_id`, targetID, userID, name).Scan(&syncDevice, &vault)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		if err != nil {
			return err
		}
		return err
	})
}
