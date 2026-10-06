package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type DeviceListRecord struct {
	VaultID   string    `json:"vaultId"`
	Version   int64     `json:"version"`
	Payload   []byte    `json:"payload"`
	Signature string    `json:"signature"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DeviceListMember is one entry of a signed list: a device id and its key.
type DeviceListMember struct {
	DeviceID  string
	PublicKey string
}

// DeviceListChange is a new signed list and the membership the server checks
// it against before storing it.
type DeviceListChange struct {
	VaultID   string
	Version   int64
	Payload   []byte
	Signature string
	Admitted  []DeviceListMember
	Revoked   []DeviceListMember
}

type DeviceGrantRecord struct {
	DeviceID           string
	PublicKey          string
	VaultID            string
	KeyEpoch           int64
	SyncDeviceID       string
	ApprovedByDeviceID string
	Payload            []byte
	Signature          string
}

func (db *Database) CurrentDeviceList(ctx context.Context, userID string) (*DeviceListRecord, error) {
	list := &DeviceListRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT vault_id,list_version,payload,signature,updated_at FROM device_lists WHERE user_id=$1`, userID).
			Scan(&list.VaultID, &list.Version, &list.Payload, &list.Signature, &list.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return list, err
}

// currentMembershipTx is what the stored list must say: admitted unified
// devices and every key ever removed.
func currentMembershipTx(ctx context.Context, tx *sql.Tx, userID string) (map[string]string, map[string]string, error) {
	admitted, revoked := map[string]string{}, map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id,public_key FROM trusted_devices WHERE user_id=$1 AND admission_state='admitted' AND identity_version=2`, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return nil, nil, err
		}
		admitted[id] = key
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT device_id,public_key FROM device_revoked_keys WHERE user_id=$1`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, nil, err
		}
		revoked[key] = id
	}
	return admitted, revoked, rows.Err()
}

func sameMembers(members []DeviceListMember, expected map[string]string) bool {
	if len(members) != len(expected) {
		return false
	}
	for _, member := range members {
		if expected[member.DeviceID] != member.PublicKey {
			return false
		}
	}
	return true
}

func sameRevoked(members []DeviceListMember, expected map[string]string) bool {
	if len(members) != len(expected) {
		return false
	}
	for _, member := range members {
		if id, ok := expected[member.PublicKey]; !ok || id != member.DeviceID {
			return false
		}
	}
	return true
}

// storeDeviceListTx locks the account's list, checks the change is exactly the
// next version over the expected membership, and stores it.
func storeDeviceListTx(ctx context.Context, tx *sql.Tx, userID string, change DeviceListChange, admitted, revoked map[string]string) error {
	var current int64
	var vault string
	err := tx.QueryRowContext(ctx, `SELECT list_version,vault_id FROM device_lists WHERE user_id=$1 FOR UPDATE`, userID).Scan(&current, &vault)
	if errors.Is(err, sql.ErrNoRows) {
		current, vault = 0, change.VaultID
	} else if err != nil {
		return err
	}
	if change.Version != current+1 || vault != change.VaultID || !sameMembers(change.Admitted, admitted) || !sameRevoked(change.Revoked, revoked) {
		return ErrDeviceListConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO device_lists(user_id,vault_id,list_version,payload,signature) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(user_id) DO UPDATE SET vault_id=EXCLUDED.vault_id,list_version=EXCLUDED.list_version,payload=EXCLUDED.payload,signature=EXCLUDED.signature,updated_at=NOW()`,
		userID, change.VaultID, change.Version, change.Payload, change.Signature)
	return err
}

// AdmitDevice stores a root-signed grant for a pending device and the new list
// that adds it, atomically. The grant names no approver when the device
// admitted itself with the sync password and secret.
func (db *Database) AdmitDevice(ctx context.Context, userID, sessionHash string, grant DeviceGrantRecord, change DeviceListChange) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return admitDeviceTx(ctx, tx, userID, sessionHash, grant, change)
	})
}

func admitDeviceTx(ctx context.Context, tx *sql.Tx, userID, sessionHash string, grant DeviceGrantRecord, change DeviceListChange) error {
	// Serialize every list change of this account on the user row.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, userID); err != nil {
		return err
	}
	var state, key string
	err := tx.QueryRowContext(ctx, `SELECT admission_state,public_key FROM trusted_devices WHERE id=$1 AND user_id=$2 AND identity_version=2 FOR UPDATE`, grant.DeviceID, userID).Scan(&state, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeviceNotFound
	}
	if err != nil {
		return err
	}
	if state == "revoked" {
		return ErrDeviceKeyRevoked
	}
	if key != grant.PublicKey {
		return ErrDeviceListConflict
	}
	var revokedKey bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_revoked_keys WHERE user_id=$1 AND public_key=$2)`, userID, key).Scan(&revokedKey); err != nil {
		return err
	}
	if revokedKey {
		return ErrDeviceKeyRevoked
	}
	if grant.ApprovedByDeviceID != "" {
		var approverAdmitted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND admission_state='admitted')`, grant.ApprovedByDeviceID, userID).Scan(&approverAdmitted); err != nil {
			return err
		}
		if !approverAdmitted {
			return ErrDeviceNotAdmitted
		}
	}
	if grant.SyncDeviceID != "" {
		var enrolled bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_devices WHERE vault_id::text=$1 AND device_id::text=$2 AND revoked_at IS NULL)`, grant.VaultID, grant.SyncDeviceID).Scan(&enrolled); err != nil {
			return err
		}
		if !enrolled {
			return ErrDeviceListConflict
		}
	}
	admitted, revoked, err := currentMembershipTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	admitted[grant.DeviceID] = grant.PublicKey
	if err := storeDeviceListTx(ctx, tx, userID, change, admitted, revoked); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE trusted_devices SET admission_state='admitted',vault_id=$3,sync_device_id=NULLIF($4,''),grant_payload=$5,grant_signature=$6,
		approved_by_device_id=NULLIF($7,''),admitted_at=NOW(),session_hash=COALESCE(NULLIF($8,''),session_hash),updated_at=NOW()
		WHERE id=$1 AND user_id=$2`, grant.DeviceID, userID, grant.VaultID, grant.SyncDeviceID, grant.Payload, grant.Signature, grant.ApprovedByDeviceID, sessionHash)
	return err
}

// RemoveDevice revokes a device everywhere: its key is refused forever, its
// sync identity and account session end and its queued jobs are canceled. An
// admitted device also needs the root-signed list that drops it.
func (db *Database) RemoveDevice(ctx context.Context, userID, targetID string, change *DeviceListChange) (sessionHash string, err error) {
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
		if state == "admitted" {
			if change == nil {
				return ErrDeviceListConflict
			}
			admitted, revoked, err := currentMembershipTx(ctx, tx, userID)
			if err != nil {
				return err
			}
			delete(admitted, targetID)
			revoked[key] = targetID
			if err := storeDeviceListTx(ctx, tx, userID, *change, admitted, revoked); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_revoked_keys(user_id,public_key,device_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, key, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE trusted_devices SET admission_state='revoked',revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW() WHERE id=$1 AND user_id=$2`, targetID, userID); err != nil {
			return err
		}
		if syncDevice.Valid && vault.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE browser_sync_devices SET revoked_at=COALESCE(revoked_at,NOW()) WHERE vault_id::text=$1 AND device_id::text=$2`, vault.String, syncDevice.String); err != nil {
				return err
			}
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
		if _, err := tx.ExecContext(ctx, `UPDATE device_admission_requests SET state='denied',updated_at=NOW() WHERE user_id=$1 AND device_id=$2 AND state IN ('pending','challenged','revealed')`, userID, targetID); err != nil {
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
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var syncDevice, vault sql.NullString
		err := tx.QueryRowContext(ctx, `UPDATE trusted_devices SET name=$3,name_updated_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND identity_version=2 AND admission_state<>'revoked' RETURNING sync_device_id,vault_id`, targetID, userID, name).Scan(&syncDevice, &vault)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		if err != nil {
			return err
		}
		if syncDevice.Valid && vault.Valid {
			_, err = tx.ExecContext(ctx, `UPDATE browser_sync_devices SET display_name=$3 WHERE vault_id::text=$1 AND device_id::text=$2 AND revoked_at IS NULL`, vault.String, syncDevice.String, name)
		}
		return err
	})
}
