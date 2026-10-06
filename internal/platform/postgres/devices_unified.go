package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Unified devices (docs/design/devices/BRIEF.md). Signatures are verified by the
// HTTP layer before any of these run; these functions keep the account's
// device records, signed list, policies and admission requests consistent.

var (
	ErrDeviceKeyRevoked     = errors.New("device key was removed from this account")
	ErrDeviceNotAdmitted    = errors.New("device is not added to this account")
	ErrDeviceListConflict   = errors.New("device list changed")
	ErrDeviceVaultMissing   = errors.New("account has no sync vault")
	ErrDeviceAdmissionState = errors.New("device admission request is not in that state")
	ErrDevicePolicyStale    = errors.New("device policy is older than the current one")
)

type DeviceRecord struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Platform           string              `json:"platform"`
	PublicKey          string              `json:"publicKey"`
	P2PEndpointID      string              `json:"p2pEndpointId,omitempty"`
	AdmissionState     string              `json:"admissionState"`
	IdentityVersion    int                 `json:"identityVersion"`
	OSVersion          string              `json:"osVersion"`
	AppVersion         string              `json:"appVersion"`
	VaultID            string              `json:"vaultId,omitempty"`
	SyncDeviceID       string              `json:"syncDeviceId,omitempty"`
	ApprovedByDeviceID string              `json:"approvedByDeviceId,omitempty"`
	AdmittedAt         *time.Time          `json:"admittedAt,omitempty"`
	LastSeenAt         time.Time           `json:"lastSeenAt"`
	CreatedAt          time.Time           `json:"createdAt"`
	RevokedAt          *time.Time          `json:"revokedAt,omitempty"`
	Policy             *DevicePolicyRecord `json:"policy,omitempty"`
}

type DevicePolicyRecord struct {
	Version       int64                `json:"version"`
	Payload       []byte               `json:"payload"`
	Signature     string               `json:"signature"`
	Files         string               `json:"files"`
	Clipboard     bool                 `json:"clipboard"`
	AgentSurfaces []string             `json:"agentSurfaces"`
	SharedFolders []DeviceSharedFolder `json:"sharedFolders"`
	UpdatedAt     time.Time            `json:"updatedAt"`
}

// DeviceSharedFolder names a folder a device shared with agents. Its local
// path never leaves that device.
type DeviceSharedFolder struct {
	ScopeID string `json:"scopeId"`
	Name    string `json:"name"`
}

type DeviceVaultRoot struct {
	VaultID       string
	RootPublicKey []byte
	KeyEpoch      int64
}

const deviceRecordColumns = `d.id,d.name,d.platform,d.public_key,COALESCE(d.p2p_endpoint_id,''),d.admission_state,d.identity_version,d.os_version,d.app_version,
	COALESCE(d.vault_id,''),COALESCE(d.sync_device_id,''),COALESCE(d.approved_by_device_id,''),d.admitted_at,d.last_seen_at,d.created_at,d.revoked_at,
	p.policy_version,p.payload,p.signature,p.files,p.clipboard,p.agent_surfaces,p.shared_folders,p.updated_at`

func scanDeviceRecord(row scanner, item *DeviceRecord) error {
	var version sql.NullInt64
	var payload []byte
	var signature, files sql.NullString
	var clipboard sql.NullBool
	var surfaces, folders []byte
	var updated sql.NullTime
	if err := row.Scan(&item.ID, &item.Name, &item.Platform, &item.PublicKey, &item.P2PEndpointID, &item.AdmissionState, &item.IdentityVersion,
		&item.OSVersion, &item.AppVersion, &item.VaultID, &item.SyncDeviceID, &item.ApprovedByDeviceID, &item.AdmittedAt, &item.LastSeenAt,
		&item.CreatedAt, &item.RevokedAt, &version, &payload, &signature, &files, &clipboard, &surfaces, &folders, &updated); err != nil {
		return err
	}
	if version.Valid {
		policy := &DevicePolicyRecord{Version: version.Int64, Payload: payload, Signature: signature.String, Files: files.String, Clipboard: clipboard.Bool, UpdatedAt: updated.Time}
		_ = json.Unmarshal(surfaces, &policy.AgentSurfaces)
		_ = json.Unmarshal(folders, &policy.SharedFolders)
		if policy.AgentSurfaces == nil {
			policy.AgentSurfaces = []string{}
		}
		if policy.SharedFolders == nil {
			policy.SharedFolders = []DeviceSharedFolder{}
		}
		item.Policy = policy
	}
	return nil
}

// AccountDevices lists every device of the account, newest policy included.
// Legacy identities (before the unified key) are omitted; they can do nothing.
func (db *Database) AccountDevices(ctx context.Context, userID string) ([]DeviceRecord, error) {
	items := []DeviceRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+deviceRecordColumns+` FROM trusted_devices d LEFT JOIN device_policies p ON p.device_id=d.id
			WHERE d.user_id=$1 AND d.identity_version=2 ORDER BY d.created_at,d.id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item DeviceRecord
			if err := scanDeviceRecord(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AccountDevice(ctx context.Context, userID, deviceID string) (*DeviceRecord, error) {
	item := &DeviceRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanDeviceRecord(tx.QueryRowContext(ctx, `SELECT `+deviceRecordColumns+` FROM trusted_devices d LEFT JOIN device_policies p ON p.device_id=d.id
			WHERE d.user_id=$1 AND d.id=$2`, userID, deviceID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}
	return item, err
}

// RegisterUnifiedDevice records a signed-in install with its proven device key
// as pending. It never revives a removed key. A legacy row that already used
// this key as its network identity is upgraded in place, keeping its id.
func (db *Database) RegisterUnifiedDevice(ctx context.Context, userID, name, publicKey, endpointID, platform, osVersion, appVersion, sessionHash string, capabilities json.RawMessage) (*DeviceRecord, error) {
	if len(capabilities) == 0 {
		capabilities = json.RawMessage(`{}`)
	}
	var id string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_revoked_keys WHERE user_id=$1 AND public_key=$2)`, userID, publicKey).Scan(&revoked); err != nil {
			return err
		}
		if revoked {
			return ErrDeviceKeyRevoked
		}
		var state string
		err := tx.QueryRowContext(ctx, `SELECT id,admission_state FROM trusted_devices WHERE user_id=$1 AND (public_key=$2 OR p2p_endpoint_id=$3)
			ORDER BY (public_key=$2) DESC LIMIT 1 FOR UPDATE`, userID, publicKey, endpointID).Scan(&id, &state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id = "device_" + uuid.NewString()
			_, err = tx.ExecContext(ctx, `INSERT INTO trusted_devices(id,user_id,name,public_key,platform,p2p_endpoint_id,device_protocol_versions,capabilities,
				admission_state,identity_version,os_version,app_version,session_hash)
				VALUES($1,$2,$3,$4,$5,$6,'["misty-device/3"]'::jsonb,$7,'pending',2,$8,$9,NULLIF($10,''))`,
				id, userID, name, publicKey, platform, endpointID, capabilities, osVersion, appVersion, sessionHash)
			return err
		case err != nil:
			return err
		case state == "revoked":
			return ErrDeviceKeyRevoked
		}
		// Possession of this key was proven, so the row it already names is this device.
		nextState := state
		if state == "legacy" {
			nextState = "pending"
		}
		_, err = tx.ExecContext(ctx, `UPDATE trusted_devices SET public_key=$3,p2p_endpoint_id=$4,platform=$5,os_version=$6,app_version=$7,capabilities=$8,
			identity_version=2,admission_state=$9,device_protocol_versions='["misty-device/3"]'::jsonb,session_hash=COALESCE(NULLIF($10,''),session_hash),
			name=CASE WHEN name_updated_at IS NULL THEN $11 ELSE name END,last_seen_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND user_id=$2`, id, userID, publicKey, endpointID, platform, osVersion, appVersion, capabilities, nextState, sessionHash, name)
		return err
	})
	var conflict *pq.Error
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		return nil, ErrDeviceIdentityConflict
	}
	if err != nil {
		return nil, err
	}
	return db.AccountDevice(ctx, userID, id)
}

// UnifiedDeviceKey returns the device key used to verify its signed requests.
// Only unified identities that were not removed have one.
func (db *Database) UnifiedDeviceKey(ctx context.Context, userID, deviceID string) (publicKey, state string, err error) {
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT public_key,admission_state FROM trusted_devices
			WHERE id=$1 AND user_id=$2 AND identity_version=2 AND revoked_at IS NULL AND admission_state IN ('pending','admitted')`, deviceID, userID).Scan(&publicKey, &state)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrDeviceNotFound
	}
	return publicKey, state, err
}

func (db *Database) DeviceVaultRoot(ctx context.Context, userID string) (*DeviceVaultRoot, error) {
	root := &DeviceVaultRoot{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT vault_id::text,root_public_key,key_epoch FROM browser_sync_vaults WHERE user_id=$1`, userID).Scan(&root.VaultID, &root.RootPublicKey, &root.KeyEpoch)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceVaultMissing
	}
	return root, err
}

func cleanDeviceText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

// CleanDeviceVersion bounds an optional version label for storage.
func CleanDeviceVersion(value string) string { return cleanDeviceText(value, 64) }
