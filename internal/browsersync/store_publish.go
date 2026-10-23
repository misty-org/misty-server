package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/cloudusage"
	"github.com/kannachi323/misty/server/internal/platform/transport"
)

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
	var workspaceMode bool
	// Selection and publication share a lock, so a takeover cannot race a write.
	err = tx.QueryRowContext(ctx, `SELECT head_sequence,key_epoch,active_device_id,active_epoch,workspace_mode FROM browser_sync_vaults WHERE user_id=$1 AND vault_id=$2 FOR UPDATE`, userID, m.VaultID).Scan(&head, &epoch, &activeDevice, &activeEpoch, &workspaceMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}

	var public []byte
	var counter int64
	var fullSync bool
	err = tx.QueryRowContext(ctx, `SELECT public_key,last_counter,full_sync FROM browser_sync_devices WHERE vault_id=$1 AND device_id=$2 AND revoked_at IS NULL FOR UPDATE`, m.VaultID, m.DeviceID).Scan(&public, &counter, &fullSync)
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
	err = tx.QueryRowContext(ctx, `SELECT content_hash,sequence,discarded FROM browser_sync_receipts WHERE vault_id=$1 AND operation_id=$2`, m.VaultID, m.OperationID).Scan(&oldHash, &oldSequence, &discarded)
	if err == nil {
		if string(oldHash) != string(digest[:]) {
			return nil, ErrSyncOperationConflict
		}
		if err = cloudusage.Commit(ctx, tx); err != nil {
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
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_control_requests r JOIN browser_sync_devices d USING(vault_id,device_id) WHERE r.vault_id=$1 AND r.operation_id=$2 AND (r.device_id<>$3 OR r.expires_at<=clock_timestamp() OR d.activation_request IS DISTINCT FROM r.operation_id))`, m.VaultID, m.OperationID, m.DeviceID).Scan(&staleRequest)
		if err != nil {
			return nil, err
		}
	}
	// In workspace mode the log carries only account-wide credentials; per-workspace
	// claims replace the single active device, and legacy clients are refused
	// at connect time, so any full-sync device may publish. Activation is gone.
	if workspaceMode && intent.Activate {
		staleRequest = true
	}
	if !fullSync || staleRequest || (!workspaceMode && !intent.Activate && activeDevice.Valid && (activeDevice.String != m.DeviceID || activeEpoch.String != intent.ActiveEpoch)) {
		// Consume the signed device counter and preserve its receipt, without
		// broadcasting stale follower data or creating a gap in the event log.
		_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_receipts(vault_id,operation_id,sequence,device_id,device_counter,content_hash,discarded) VALUES($1,$2,$3,$4,$5,$6,true)`, m.VaultID, m.OperationID, head, m.DeviceID, m.DeviceCounter, digest[:])
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE browser_sync_devices SET last_counter=$3 WHERE vault_id=$1 AND device_id=$2`, m.VaultID, m.DeviceID, m.DeviceCounter)
		if err != nil {
			return nil, err
		}
		if err = cloudusage.Commit(ctx, tx); err != nil {
			return nil, err
		}
		return &SyncReceipt{OperationID: m.OperationID, Sequence: head, Discarded: true}, nil
	}
	if head >= SyncMaxCounter {
		return nil, ErrSyncInvalid
	}
	sequence := head + 1
	envelope, _ := json.Marshal(m.Envelope)
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_events(vault_id,sequence,operation_id,device_id,device_counter,key_epoch,envelope,signature) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, m.VaultID, sequence, m.OperationID, m.DeviceID, m.DeviceCounter, m.KeyEpoch, string(envelope), m.Signature)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_receipts(vault_id,operation_id,sequence,device_id,device_counter,content_hash) VALUES($1,$2,$3,$4,$5,$6)`, m.VaultID, m.OperationID, sequence, m.DeviceID, m.DeviceCounter, digest[:])
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE browser_sync_devices SET last_counter=$3 WHERE vault_id=$1 AND device_id=$2`, m.VaultID, m.DeviceID, m.DeviceCounter)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE browser_sync_vaults SET head_sequence=$2 WHERE vault_id=$1`, m.VaultID, sequence)
	if err != nil {
		return nil, err
	}
	if intent.Activate {
		_, err = tx.ExecContext(ctx, `UPDATE browser_sync_vaults SET active_device_id=$2,active_epoch=$3,active_seen_at=clock_timestamp() WHERE vault_id=$1`, m.VaultID, m.DeviceID, m.OperationID)
		if err != nil {
			return nil, err
		}
	}
	hint, _ := json.Marshal(transport.AccountEvent{UserID: userID, Topic: "browser-sync", ID: m.VaultID})
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('misty_account_events',$1)`, string(hint))
	if err != nil {
		return nil, err
	}
	if err = cloudusage.Commit(ctx, tx); err != nil {
		return nil, err
	}
	return &SyncReceipt{OperationID: m.OperationID, Sequence: sequence}, nil
}
