package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/platform/transport"

	"github.com/google/uuid"
)

var (
	ErrSyncInvalid           = errors.New("invalid sync request")
	ErrSyncForbidden         = errors.New("sync device forbidden")
	ErrSyncExists            = errors.New("sync workspace already exists")
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
	if e.Version != 1 || e.KDF != "argon2id-m65536-t3-p1" || len(e.Salt) != 24 {
		return false
	}
	salt, err := base64.StdEncoding.Strict().DecodeString(e.Salt)
	return err == nil && len(salt) == 16 && (SyncEnvelope{e.Version, e.Nonce, e.Ciphertext}).Validate(4096)
}

type SyncWorkspace struct {
	WorkspaceID   string          `json:"workspace_id"`
	KeyEpoch      int64           `json:"key_epoch"`
	HeadSequence  int64           `json:"head_sequence"`
	RootPublicKey []byte          `json:"root_public_key"`
	KeyEnvelope   json.RawMessage `json:"key_envelope"`
}

type SyncDeviceGrant struct {
	WorkspaceID string `json:"workspace_id"`
	DeviceID    string `json:"device_id"`
	KeyEpoch    int64  `json:"key_epoch"`
	PublicKey   []byte `json:"public_key"`
	Signature   []byte `json:"signature"`
}

func (g SyncDeviceGrant) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.device.v1", g.WorkspaceID, g.DeviceID, g.KeyEpoch, base64.StdEncoding.EncodeToString(g.PublicKey)})
	return data
}
func validSyncID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}
func (g SyncDeviceGrant) valid(root []byte) bool {
	return validSyncID(g.WorkspaceID) && validSyncID(g.DeviceID) && g.KeyEpoch > 0 && g.KeyEpoch <= SyncMaxCounter && len(root) == ed25519.PublicKeySize && len(g.PublicKey) == ed25519.PublicKeySize && len(g.Signature) == ed25519.SignatureSize && ed25519.Verify(root, g.SigningBytes(), g.Signature)
}

type SyncMutation struct {
	WorkspaceID   string       `json:"workspace_id"`
	OperationID   string       `json:"operation_id"`
	DeviceID      string       `json:"device_id"`
	DeviceCounter int64        `json:"device_counter"`
	KeyEpoch      int64        `json:"key_epoch"`
	Envelope      SyncEnvelope `json:"envelope"`
	Signature     []byte       `json:"signature"`
}

func (m SyncMutation) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.mutation.v1", m.WorkspaceID, m.OperationID, m.DeviceID, m.DeviceCounter, m.KeyEpoch, m.Envelope.Version, m.Envelope.Nonce, m.Envelope.Ciphertext})
	return data
}
func (m SyncMutation) Valid() bool {
	return validSyncID(m.WorkspaceID) && validSyncID(m.OperationID) && validSyncID(m.DeviceID) && m.DeviceCounter > 0 && m.DeviceCounter <= SyncMaxCounter && m.KeyEpoch > 0 && m.KeyEpoch <= SyncMaxCounter && len(m.Signature) == ed25519.SignatureSize && m.Envelope.Validate(SyncMaxEventBytes)
}

type SyncReceipt struct {
	OperationID string `json:"operation_id"`
	Sequence    int64  `json:"sequence"`
	Discarded   bool   `json:"discarded,omitempty"`
	Reason      string `json:"reason,omitempty"`
	TreeVersion int64  `json:"tree_version,omitempty"`
}
type SyncEvent struct {
	SyncMutation
	Sequence int64 `json:"sequence"`
}
type SyncReplay struct {
	HeadSequence       int64       `json:"head_sequence"`
	Events             []SyncEvent `json:"events"`
	CheckpointRequired bool        `json:"checkpoint_required"`
}

func (db *Store) BrowserSyncWorkspace(ctx context.Context, userID string) (*SyncWorkspace, error) {
	var w SyncWorkspace
	err := db.Conn.QueryRowContext(ctx, `SELECT workspace_id,key_epoch,head_sequence,root_public_key,key_envelope FROM browser_sync_workspaces WHERE user_id=$1`, userID).Scan(&w.WorkspaceID, &w.KeyEpoch, &w.HeadSequence, &w.RootPublicKey, &w.KeyEnvelope)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &w, err
}

// Bootstrap and enrollment require a vault-root signature. Account access alone
// cannot add a device that clients would trust to write encrypted state.
func (db *Store) CreateBrowserSyncWorkspace(ctx context.Context, userID string, root []byte, keyEnvelope SyncKeyEnvelope, grant SyncDeviceGrant) error {
	if grant.KeyEpoch != 1 || !grant.valid(root) || !keyEnvelope.Valid() {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	wrapped, _ := json.Marshal(keyEnvelope)
	result, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_workspaces(user_id,workspace_id,root_public_key,key_envelope) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, userID, grant.WorkspaceID, root, string(wrapped))
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
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_devices(workspace_id,device_id,public_key,grant_epoch,grant_signature) VALUES($1,$2,$3,$4,$5)`, grant.WorkspaceID, grant.DeviceID, grant.PublicKey, grant.KeyEpoch, grant.Signature)
	if err != nil {
		return err
	}
	if err = ensureBrowserSyncTrees(ctx, tx, grant.WorkspaceID, grant.DeviceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *Store) EnrollBrowserSyncDevice(ctx context.Context, userID string, g SyncDeviceGrant) error {
	if !validSyncID(g.WorkspaceID) || !validSyncID(g.DeviceID) {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var root []byte
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT root_public_key,key_epoch FROM browser_sync_workspaces WHERE user_id=$1 AND workspace_id=$2 FOR UPDATE`, userID, g.WorkspaceID).Scan(&root, &epoch)
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
	result, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_devices(workspace_id,device_id,public_key,grant_epoch,grant_signature) VALUES($1,$2,$3,$4,$5) ON CONFLICT(workspace_id,device_id) DO UPDATE SET grant_epoch=EXCLUDED.grant_epoch,grant_signature=EXCLUDED.grant_signature WHERE browser_sync_devices.revoked_at IS NULL AND browser_sync_devices.public_key=EXCLUDED.public_key`, g.WorkspaceID, g.DeviceID, g.PublicKey, g.KeyEpoch, g.Signature)
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
	if err = ensureBrowserSyncTrees(ctx, tx, g.WorkspaceID, g.DeviceID); err != nil {
		return err
	}
	return tx.Commit()
}

type SyncPublishOptions struct {
	Activate    bool
	ActiveEpoch string
}

func (db *Store) PublishBrowserSync(ctx context.Context, userID string, m SyncMutation, options ...SyncPublishOptions) (*SyncReceipt, error) {
	var intent SyncPublishOptions
	if len(options) > 0 {
		intent = options[0]
	}
	if !m.Valid() {
		return nil, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var head, epoch int64
	var activeDevice, activeEpoch sql.NullString
	var treeMode bool
	// Selection and publication share a lock, so a takeover cannot race a write.
	err = tx.QueryRowContext(ctx, `SELECT head_sequence,key_epoch,active_device_id,active_epoch,tree_mode FROM browser_sync_workspaces WHERE user_id=$1 AND workspace_id=$2 FOR UPDATE`, userID, m.WorkspaceID).Scan(&head, &epoch, &activeDevice, &activeEpoch, &treeMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}

	var public []byte
	var counter int64
	var fullSync bool
	err = tx.QueryRowContext(ctx, `SELECT public_key,last_counter,full_sync FROM browser_sync_devices WHERE workspace_id=$1 AND device_id=$2 AND revoked_at IS NULL FOR UPDATE`, m.WorkspaceID, m.DeviceID).Scan(&public, &counter, &fullSync)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	signed := m.SigningBytes()
	if len(public) != ed25519.PublicKeySize || !ed25519.Verify(public, signed, m.Signature) {
		return nil, ErrSyncForbidden
	}
	digest := sha256.Sum256(signed)
	var oldHash []byte
	var oldSequence int64
	var discarded bool
	err = tx.QueryRowContext(ctx, `SELECT content_hash,sequence,discarded FROM browser_sync_receipts WHERE workspace_id=$1 AND operation_id=$2`, m.WorkspaceID, m.OperationID).Scan(&oldHash, &oldSequence, &discarded)
	if err == nil {
		if string(oldHash) != string(digest[:]) {
			return nil, ErrSyncOperationConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return &SyncReceipt{OperationID: m.OperationID, Sequence: oldSequence, Discarded: discarded}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if epoch != m.KeyEpoch {
		return nil, ErrSyncEpoch
	}
	if m.DeviceCounter <= counter {
		return nil, ErrSyncCompacted
	}
	if m.DeviceCounter != counter+1 {
		return nil, ErrSyncCounterGap
	}
	var staleRequest bool
	if intent.Activate {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_control_requests r JOIN browser_sync_devices d USING(workspace_id,device_id) WHERE r.workspace_id=$1 AND r.operation_id=$2 AND (r.device_id<>$3 OR r.expires_at<=clock_timestamp() OR d.activation_request IS DISTINCT FROM r.operation_id))`, m.WorkspaceID, m.OperationID, m.DeviceID).Scan(&staleRequest)
		if err != nil {
			return nil, err
		}
	}
	// In tree mode the log carries only account-wide credentials; per-tree
	// claims replace the single active device, and legacy clients are refused
	// at connect time, so any full-sync device may publish. Activation is gone.
	if treeMode && intent.Activate {
		staleRequest = true
	}
	if !fullSync || staleRequest || (!treeMode && !intent.Activate && activeDevice.Valid && (activeDevice.String != m.DeviceID || activeEpoch.String != intent.ActiveEpoch)) {
		// Consume the signed device counter and preserve its receipt, without
		// broadcasting stale follower data or creating a gap in the event log.
		_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_receipts(workspace_id,operation_id,sequence,device_id,device_counter,content_hash,discarded) VALUES($1,$2,$3,$4,$5,$6,true)`, m.WorkspaceID, m.OperationID, head, m.DeviceID, m.DeviceCounter, digest[:])
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE browser_sync_devices SET last_counter=$3 WHERE workspace_id=$1 AND device_id=$2`, m.WorkspaceID, m.DeviceID, m.DeviceCounter)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return &SyncReceipt{OperationID: m.OperationID, Sequence: head, Discarded: true}, nil
	}
	if head >= SyncMaxCounter {
		return nil, ErrSyncInvalid
	}
	sequence := head + 1
	envelope, _ := json.Marshal(m.Envelope)
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_events(workspace_id,sequence,operation_id,device_id,device_counter,key_epoch,envelope,signature) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, m.WorkspaceID, sequence, m.OperationID, m.DeviceID, m.DeviceCounter, m.KeyEpoch, string(envelope), m.Signature)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_receipts(workspace_id,operation_id,sequence,device_id,device_counter,content_hash) VALUES($1,$2,$3,$4,$5,$6)`, m.WorkspaceID, m.OperationID, sequence, m.DeviceID, m.DeviceCounter, digest[:])
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE browser_sync_devices SET last_counter=$3 WHERE workspace_id=$1 AND device_id=$2`, m.WorkspaceID, m.DeviceID, m.DeviceCounter)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE browser_sync_workspaces SET head_sequence=$2 WHERE workspace_id=$1`, m.WorkspaceID, sequence)
	if err != nil {
		return nil, err
	}
	if intent.Activate {
		_, err = tx.ExecContext(ctx, `UPDATE browser_sync_workspaces SET active_device_id=$2,active_epoch=$3,active_seen_at=clock_timestamp() WHERE workspace_id=$1`, m.WorkspaceID, m.DeviceID, m.OperationID)
		if err != nil {
			return nil, err
		}
	}
	hint, _ := json.Marshal(transport.AccountEvent{UserID: userID, Topic: "browser-sync", ID: m.WorkspaceID})
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('misty_account_events',$1)`, string(hint))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &SyncReceipt{OperationID: m.OperationID, Sequence: sequence}, nil
}

// A consistent read prevents compaction from creating a false gap between the
// head read and the event query. Reads require an enrolled, non-revoked device.
func (db *Store) ReplayBrowserSync(ctx context.Context, userID, workspaceID, deviceID string, after int64, limit int) (*SyncReplay, error) {
	if !validSyncID(workspaceID) || !validSyncID(deviceID) || after < 0 || limit < 1 || limit > 200 {
		return nil, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &SyncReplay{Events: []SyncEvent{}}
	err = tx.QueryRowContext(ctx, `SELECT w.head_sequence FROM browser_sync_workspaces w JOIN browser_sync_devices d USING(workspace_id) WHERE w.user_id=$1 AND w.workspace_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL`, userID, workspaceID, deviceID).Scan(&out.HeadSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	if after > out.HeadSequence {
		return nil, ErrSyncCursor
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,operation_id,device_id,device_counter,key_epoch,envelope,signature FROM browser_sync_events WHERE workspace_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, workspaceID, after, limit)
	if err != nil {
		return nil, err
	}
	totalBytes := 0
	for rows.Next() {
		e := SyncEvent{}
		e.WorkspaceID = workspaceID
		var raw []byte
		if err = rows.Scan(&e.Sequence, &e.OperationID, &e.DeviceID, &e.DeviceCounter, &e.KeyEpoch, &raw, &e.Signature); err != nil {
			rows.Close()
			return nil, err
		}
		if e.Sequence != after+int64(len(out.Events))+1 {
			out.CheckpointRequired = true
			out.Events = []SyncEvent{}
			break
		}
		// Bound replay memory and frame size even when every event reaches its cap.
		if len(out.Events) > 0 && totalBytes+len(raw) > 4<<20 {
			break
		}
		if err = json.Unmarshal(raw, &e.Envelope); err != nil {
			rows.Close()
			return nil, err
		}
		out.Events = append(out.Events, e)
		totalBytes += len(raw)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out.Events) == 0 && after < out.HeadSequence {
		out.CheckpointRequired = true
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
