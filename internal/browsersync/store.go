package browsersync

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrSyncInvalid           = errors.New("invalid sync request")
	ErrSyncForbidden         = errors.New("sync device forbidden")
	ErrSyncExists            = errors.New("sync vault already exists")
	ErrSyncEpoch             = errors.New("sync key epoch changed")
	ErrSyncCounterGap        = errors.New("sync device counter gap")
	ErrSyncOperationConflict = errors.New("sync operation conflict")
	ErrSyncCompacted         = errors.New("sync operation already compacted")
	ErrSyncCursor            = errors.New("invalid sync cursor")
)

const SyncMaxCounter int64 = 9007199254740991
const SyncMaxEventBytes = 1 << 20

// Field order is protocol-defined for signature encoding; never sign JSON maps.
type SyncEnvelope struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func (e SyncEnvelope) Validate(limit int) bool {
	if e.Version != 1 || len(e.Nonce) != 16 || len(e.Ciphertext) > ((limit+2)/3)*4 {
		return false
	}
	nonce, a := base64.StdEncoding.Strict().DecodeString(e.Nonce)
	cipher, b := base64.StdEncoding.Strict().DecodeString(e.Ciphertext)
	return a == nil && b == nil && len(nonce) == 12 && len(cipher) >= 16 && len(cipher) <= limit
}

// Only a versioned encrypted key wrapper is accepted at the storage boundary.
type SyncKeyEnvelope struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func (e SyncKeyEnvelope) Valid() bool {
	if e.Version != 1 || (e.KDF != SyncKDFLegacy && e.KDF != SyncKDFAccount) || len(e.Salt) != 24 {
		return false
	}
	salt, err := base64.StdEncoding.Strict().DecodeString(e.Salt)
	return err == nil && len(salt) == 16 && (SyncEnvelope{e.Version, e.Nonce, e.Ciphertext}).Validate(4096)
}

type SyncVault struct {
	VaultID       string `json:"vault_id"`
	KeyEpoch      int64  `json:"key_epoch"`
	HeadSequence  int64  `json:"head_sequence"`
	RootPublicKey []byte `json:"root_public_key"`
	// Exactly one is set: the personal vault's password wrapper, or another
	// Space vault's root sealed to the personal root.
	KeyEnvelope      json.RawMessage `json:"key_envelope,omitempty"`
	SpaceKeyEnvelope json.RawMessage `json:"space_key_envelope,omitempty"`
	// Present once the password wrapper has been replaced after creation.
	KeyTransition *SyncKeyTransition `json:"key_transition,omitempty"`
}

type SyncDeviceGrant struct {
	VaultID   string `json:"vault_id"`
	DeviceID  string `json:"device_id"`
	KeyEpoch  int64  `json:"key_epoch"`
	PublicKey []byte `json:"public_key"`
	Signature []byte `json:"signature"`
}

func (g SyncDeviceGrant) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.device.v1", g.VaultID, g.DeviceID, g.KeyEpoch, base64.StdEncoding.EncodeToString(g.PublicKey)})
	return data
}
func validSyncID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}
func (g SyncDeviceGrant) valid(root []byte) bool {
	return validSyncID(g.VaultID) && validSyncID(g.DeviceID) && g.KeyEpoch > 0 && g.KeyEpoch <= SyncMaxCounter && len(root) == ed25519.PublicKeySize && len(g.PublicKey) == ed25519.PublicKeySize && len(g.Signature) == ed25519.SignatureSize && ed25519.Verify(root, g.SigningBytes(), g.Signature)
}

type SyncMutation struct {
	VaultID       string       `json:"vault_id"`
	OperationID   string       `json:"operation_id"`
	DeviceID      string       `json:"device_id"`
	DeviceCounter int64        `json:"device_counter"`
	KeyEpoch      int64        `json:"key_epoch"`
	Envelope      SyncEnvelope `json:"envelope"`
	Signature     []byte       `json:"signature"`
}

func (m SyncMutation) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.mutation.v1", m.VaultID, m.OperationID, m.DeviceID, m.DeviceCounter, m.KeyEpoch, m.Envelope.Version, m.Envelope.Nonce, m.Envelope.Ciphertext})
	return data
}
func (m SyncMutation) Valid() bool {
	return validSyncID(m.VaultID) && validSyncID(m.OperationID) && validSyncID(m.DeviceID) && m.DeviceCounter > 0 && m.DeviceCounter <= SyncMaxCounter && m.KeyEpoch > 0 && m.KeyEpoch <= SyncMaxCounter && len(m.Signature) == ed25519.SignatureSize && m.Envelope.Validate(SyncMaxEventBytes)
}

type SyncReceipt struct {
	OperationID      string `json:"operation_id"`
	Sequence         int64  `json:"sequence"`
	Discarded        bool   `json:"discarded,omitempty"`
	Reason           string `json:"reason,omitempty"`
	WorkspaceVersion int64  `json:"workspace_version,omitempty"`
}

func (db *Store) EnrollBrowserSyncDevice(ctx context.Context, userID string, g SyncDeviceGrant) error {
	if !validSyncID(g.VaultID) || !validSyncID(g.DeviceID) {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var root []byte
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT root_public_key,key_epoch FROM browser_sync_vaults WHERE user_id=$1 AND vault_id=$2 FOR UPDATE`, userID, g.VaultID).Scan(&root, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	if epoch != g.KeyEpoch {
		return ErrSyncEpoch
	}
	if !g.valid(root) {
		return ErrSyncForbidden
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_devices(vault_id,device_id,public_key,grant_epoch,grant_signature) VALUES($1,$2,$3,$4,$5) ON CONFLICT(vault_id,device_id) DO UPDATE SET grant_epoch=EXCLUDED.grant_epoch,grant_signature=EXCLUDED.grant_signature WHERE browser_sync_devices.revoked_at IS NULL AND browser_sync_devices.public_key=EXCLUDED.public_key`, g.VaultID, g.DeviceID, g.PublicKey, g.KeyEpoch, g.Signature)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSyncForbidden
	}
	if err = ensureBrowserSyncWorkspaces(ctx, tx, g.VaultID, g.DeviceID); err != nil {
		return err
	}
	if err = notifySync(ctx, tx, userID, g.VaultID, "browser-presence"); err != nil {
		return err
	}
	return tx.Commit()
}
