package browsersync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/kannachi323/misty/server/internal/cloudusage"
)

// A person holds one vault per Space they belong to. The personal Space's
// vault keeps the password-wrapped root; every other vault's root is sealed to
// the personal root, so one sync password opens them all.
type SyncSpaceVault struct {
	SpaceID string `json:"space_id"`
	SyncVault
}

// SyncVaultKeys carries exactly one way to open a new vault's root.
type SyncVaultKeys struct {
	KeyEnvelope      *SyncKeyEnvelope `json:"key_envelope,omitempty"`
	SpaceKeyEnvelope *SyncEnvelope    `json:"space_key_envelope,omitempty"`
}

// PasswordVaultKeys opens the personal vault with the sync password.
func PasswordVaultKeys(e SyncKeyEnvelope) SyncVaultKeys { return SyncVaultKeys{KeyEnvelope: &e} }

// SpaceVaultKeys opens a Space vault with the personal root.
func SpaceVaultKeys(e SyncEnvelope) SyncVaultKeys { return SyncVaultKeys{SpaceKeyEnvelope: &e} }

func (k SyncVaultKeys) valid() bool {
	if (k.KeyEnvelope == nil) == (k.SpaceKeyEnvelope == nil) {
		return false
	}
	if k.KeyEnvelope != nil {
		return k.KeyEnvelope.Valid()
	}
	return k.SpaceKeyEnvelope.Validate(4096)
}

const syncVaultColumns = `v.space_id,v.vault_id,v.key_epoch,v.head_sequence,v.root_public_key,v.key_envelope,v.space_key_envelope`

func scanSyncVault(row interface{ Scan(...any) error }) (*SyncSpaceVault, error) {
	var v SyncSpaceVault
	var key, space []byte
	if err := row.Scan(&v.SpaceID, &v.VaultID, &v.KeyEpoch, &v.HeadSequence, &v.RootPublicKey, &key, &space); err != nil {
		return nil, err
	}
	v.KeyEnvelope, v.SpaceKeyEnvelope = key, space
	return &v, nil
}

func (db *Store) oneSyncVault(ctx context.Context, where string, args ...any) (*SyncSpaceVault, error) {
	v, err := scanSyncVault(db.Conn.QueryRowContext(ctx, `SELECT `+syncVaultColumns+` FROM browser_sync_vaults v WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

// BrowserSyncVault returns the account's personal vault, the one its sync
// password opens.
func (db *Store) BrowserSyncVault(ctx context.Context, userID string) (*SyncVault, error) {
	v, err := db.oneSyncVault(ctx, `v.user_id=$1 AND v.key_envelope IS NOT NULL`, userID)
	if v == nil || err != nil {
		return nil, err
	}
	return &v.SyncVault, nil
}

// BrowserSyncVaultByID returns one of the account's vaults.
func (db *Store) BrowserSyncVaultByID(ctx context.Context, userID, vaultID string) (*SyncVault, error) {
	if !validSyncID(vaultID) {
		return nil, ErrSyncInvalid
	}
	v, err := db.oneSyncVault(ctx, `v.user_id=$1 AND v.vault_id=$2`, userID, vaultID)
	if v == nil || err != nil {
		return nil, err
	}
	return &v.SyncVault, nil
}

// BrowserSyncVaultForSpace returns the account's vault for one Space.
func (db *Store) BrowserSyncVaultForSpace(ctx context.Context, userID, spaceID string) (*SyncSpaceVault, error) {
	return db.oneSyncVault(ctx, `v.user_id=$1 AND v.space_id=$2`, userID, spaceID)
}

// BrowserSyncVaults lists every vault the account holds, the personal one first.
func (db *Store) BrowserSyncVaults(ctx context.Context, userID string) ([]SyncSpaceVault, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT `+syncVaultColumns+` FROM browser_sync_vaults v WHERE v.user_id=$1 ORDER BY v.key_envelope IS NULL, v.created_at, v.vault_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncSpaceVault{}
	for rows.Next() {
		v, err := scanSyncVault(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// CreateBrowserSyncVault creates the account's vault for a Space it belongs
// to; an empty spaceID names the personal Space. The personal vault takes the
// password wrapper and every other vault a root sealed to the personal root.
// Bootstrap and enrollment require a vault-root signature: account access
// alone cannot add a device that clients would trust to write encrypted state.
// A Space vault is granted only to a device already admitted to the personal
// vault, under the same device key.
func (db *Store) CreateBrowserSyncVault(ctx context.Context, userID, spaceID string, root []byte, keys SyncVaultKeys, grant SyncDeviceGrant) error {
	if grant.KeyEpoch != 1 || !grant.valid(root) || !keys.valid() {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if spaceID == "" {
		var personal sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT misty_ensure_personal_space($1)`, userID).Scan(&personal); err != nil {
			return err
		}
		if !personal.Valid {
			return ErrSyncForbidden
		}
		spaceID = personal.String
	}
	// Space membership is guarded by row security; this check reads it as the
	// service, for this transaction only.
	if _, err = tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
		return err
	}
	var isDefault bool
	err = tx.QueryRowContext(ctx, `SELECT s.is_default AND s.owner_user_id=$2 FROM space_members m JOIN spaces s ON s.id=m.space_id
		WHERE m.space_id=$1 AND m.user_id=$2 AND s.lifecycle_state='active' FOR SHARE OF m`, spaceID, userID).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	if isDefault != (keys.KeyEnvelope != nil) {
		return ErrSyncInvalid
	}
	var key, space any
	if keys.KeyEnvelope != nil {
		wrapped, _ := json.Marshal(keys.KeyEnvelope)
		key = string(wrapped)
	} else {
		var admitted bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_vaults v JOIN browser_sync_devices d USING(vault_id)
			WHERE v.user_id=$1 AND v.key_envelope IS NOT NULL AND d.device_id=$2 AND d.public_key=$3 AND d.revoked_at IS NULL)`, userID, grant.DeviceID, grant.PublicKey).Scan(&admitted)
		if err != nil {
			return err
		}
		if !admitted {
			return ErrSyncForbidden
		}
		sealed, _ := json.Marshal(keys.SpaceKeyEnvelope)
		space = string(sealed)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_vaults(user_id,space_id,vault_id,root_public_key,key_envelope,space_key_envelope) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, userID, spaceID, grant.VaultID, root, key, space)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSyncExists
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_devices(vault_id,device_id,public_key,grant_epoch,grant_signature) VALUES($1,$2,$3,$4,$5)`, grant.VaultID, grant.DeviceID, grant.PublicKey, grant.KeyEpoch, grant.Signature)
	if err != nil {
		return err
	}
	if err = ensureBrowserSyncWorkspaces(ctx, tx, grant.VaultID, grant.DeviceID); err != nil {
		return err
	}
	return cloudusage.Commit(ctx, tx)
}

// DeleteBrowserSyncVaults deletes every vault the account holds: the personal
// one and each Space vault sealed to its root, which nothing could open once
// that root is gone. Their devices, events, records and blobs go with them;
// the server only ever held their ciphertext. Reports whether a personal vault
// existed, so the account can create a new one afterwards.
func (db *Store) DeleteBrowserSyncVaults(ctx context.Context, userID string) (bool, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var personal bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_vaults WHERE user_id=$1 AND key_envelope IS NOT NULL)`, userID).Scan(&personal); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM browser_sync_vaults WHERE user_id=$1`, userID); err != nil {
		return false, err
	}
	return personal, cloudusage.Commit(ctx, tx)
}
