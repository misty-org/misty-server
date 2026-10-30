package browsersync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type SyncEvent struct {
	SyncMutation
	Sequence int64 `json:"sequence"`
}

type SyncReplay struct {
	HeadSequence       int64       `json:"head_sequence"`
	Events             []SyncEvent `json:"events"`
	CheckpointRequired bool        `json:"checkpoint_required"`
}

// A consistent read prevents compaction from creating a false gap between the
// head read and the event query. Reads require an enrolled, non-revoked device.
func (db *Store) ReplayBrowserSync(ctx context.Context, userID, vaultID, deviceID string, after int64, limit int) (*SyncReplay, error) {
	if !validSyncID(vaultID) || !validSyncID(deviceID) || after < 0 || limit < 1 || limit > 200 {
		return nil, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &SyncReplay{Events: []SyncEvent{}}
	err = tx.QueryRowContext(ctx, `SELECT w.head_sequence FROM browser_sync_vaults w JOIN browser_sync_devices d USING(vault_id) WHERE w.user_id=$1 AND w.vault_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL`, userID, vaultID, deviceID).Scan(&out.HeadSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	if after > out.HeadSequence {
		return nil, ErrSyncCursor
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,operation_id,device_id,device_counter,key_epoch,envelope,signature FROM browser_sync_events WHERE vault_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, vaultID, after, limit)
	if err != nil {
		return nil, err
	}
	totalBytes := 0
	for rows.Next() {
		e := SyncEvent{}
		e.VaultID = vaultID
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
