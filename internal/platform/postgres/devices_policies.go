package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// StoreDevicePolicy keeps a device's own signed permissions. Versions only
// move forward, so a replayed older policy cannot undo a newer one.
func (db *Database) StoreDevicePolicy(ctx context.Context, userID, deviceID string, policy DevicePolicyRecord) error {
	surfaces, _ := json.Marshal(policy.AgentSurfaces)
	if policy.SharedFolders == nil {
		policy.SharedFolders = []DeviceSharedFolder{}
	}
	folders, _ := json.Marshal(policy.SharedFolders)
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO device_policies(device_id,user_id,policy_version,payload,signature,files,clipboard,agent_surfaces,shared_folders)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND identity_version=2 AND admission_state IN ('pending','admitted')
			ON CONFLICT(device_id) DO UPDATE SET policy_version=EXCLUDED.policy_version,payload=EXCLUDED.payload,signature=EXCLUDED.signature,files=EXCLUDED.files,
				clipboard=EXCLUDED.clipboard,agent_surfaces=EXCLUDED.agent_surfaces,shared_folders=EXCLUDED.shared_folders,updated_at=NOW()
			WHERE device_policies.policy_version < EXCLUDED.policy_version`,
			deviceID, userID, policy.Version, policy.Payload, policy.Signature, policy.Files, policy.Clipboard, surfaces, folders)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrDevicePolicyStale
		}
		return nil
	})
}

// DevicePolicyAllows reports whether a device's current signed policy lets
// agents use a surface there; for folders, the named folder must be shared.
func (db *Database) DevicePolicyAllows(ctx context.Context, userID, deviceID, surface, scopeID string) (bool, error) {
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_policies WHERE device_id=$1 AND user_id=$2 AND agent_surfaces ? $3
			AND ($3<>'folders' OR EXISTS(SELECT 1 FROM jsonb_array_elements(shared_folders) f WHERE f->>'scopeId'=$4)))`, deviceID, userID, surface, scopeID).Scan(&allowed)
	})
	return allowed, err
}

// DeviceClipboardEnabled reports whether a device's latest signed policy turns
// the shared clipboard on. A device with no policy yet has it off.
func (db *Database) DeviceClipboardEnabled(ctx context.Context, userID, deviceID string) (bool, error) {
	enabled := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_policies WHERE device_id=$1 AND user_id=$2 AND clipboard)`, deviceID, userID).Scan(&enabled)
	})
	return enabled, err
}
