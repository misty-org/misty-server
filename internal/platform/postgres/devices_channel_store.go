package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

// CreateDeviceChannelTicket issues a one-use, 60-second socket ticket for a
// unified device. Removed and legacy devices get none.
func (db *Database) CreateDeviceChannelTicket(ctx context.Context, userID, deviceID, tokenHash, sessionHash string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM device_channel_tickets WHERE expires_at<=NOW() OR device_id=$1`, deviceID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO device_channel_tickets(token_hash,user_id,device_id,session_hash)
			SELECT $1,$2,$3,$4 FROM trusted_devices WHERE id=$3 AND user_id=$2 AND identity_version=2 AND admission_state IN ('pending','admitted')`, tokenHash, userID, deviceID, sessionHash)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrDeviceNotFound
		}
		if sessionHash != "" {
			_, err = tx.ExecContext(ctx, `UPDATE trusted_devices SET session_hash=$3 WHERE id=$1 AND user_id=$2`, deviceID, userID, sessionHash)
		}
		return err
	})
}

type DeviceChannelIdentity struct {
	UserID      string
	DeviceID    string
	PublicKey   string
	State       string
	SessionHash string
}

func (db *Database) ConsumeDeviceChannelTicket(ctx context.Context, tokenHash string) (*DeviceChannelIdentity, error) {
	identity := &DeviceChannelIdentity{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH consumed AS (DELETE FROM device_channel_tickets WHERE token_hash=$1 AND expires_at>NOW() RETURNING user_id,device_id,session_hash)
			SELECT c.user_id,c.device_id,d.public_key,d.admission_state,c.session_hash FROM consumed c JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=c.user_id
			WHERE d.identity_version=2 AND d.admission_state IN ('pending','admitted') AND d.revoked_at IS NULL`, tokenHash).
			Scan(&identity.UserID, &identity.DeviceID, &identity.PublicKey, &identity.State, &identity.SessionHash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}
	return identity, err
}

// DeviceAdmissionState is re-read by long-lived channels after a list change.
func (db *Database) DeviceAdmissionState(ctx context.Context, userID, deviceID string) (string, error) {
	state := ""
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT admission_state FROM trusted_devices WHERE id=$1 AND user_id=$2`, deviceID, userID).Scan(&state)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "revoked", nil
	}
	return state, err
}

// MarkDevicesSeen keeps the 90-second online window that device jobs check
// current for every socket this process holds, in one statement however many
// devices are connected. Steady-state renewals notify nothing.
func (db *Database) MarkDevicesSeen(ctx context.Context, deviceIDs []string) error {
	if len(deviceIDs) == 0 {
		return nil
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE trusted_devices SET last_seen_at=NOW() WHERE id=ANY($1) AND revoked_at IS NULL AND admission_state IN ('pending','admitted')`, pq.Array(deviceIDs))
		return err
	})
}
