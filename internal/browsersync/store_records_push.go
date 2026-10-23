package browsersync

import (
	"context"
	"database/sql"
	"errors"
	"github.com/kannachi323/misty/server/internal/cloudusage"
)

// PushBrowserSyncRecords applies each write whose base version matches the
// stored record, and returns the current row for each that lost.
func (db *Store) PushBrowserSyncRecords(ctx context.Context, userID, vault, device, collection string, writes []SyncRecordWrite) ([]SyncRecordResult, int64, error) {
	if !syncRecordCollections[collection] || len(writes) == 0 || len(writes) > syncRecordMaxWrites {
		return nil, 0, ErrSyncInvalid
	}
	for _, w := range writes {
		if !validRecordWrite(collection, w) {
			return nil, 0, ErrSyncInvalid
		}
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var fullSync bool
	err = tx.QueryRowContext(ctx, `SELECT d.full_sync FROM browser_sync_vaults w JOIN browser_sync_devices d USING(vault_id) WHERE w.user_id=$1 AND w.vault_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL`, userID, vault, device).Scan(&fullSync)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrSyncForbidden
	}
	if err != nil {
		return nil, 0, err
	}
	if !fullSync {
		return nil, 0, ErrSyncForbidden
	}
	clipboard := collection == syncClipboardCollection
	if clipboard {
		allowed, err := clipboardAllowed(ctx, tx, userID, device)
		if err != nil {
			return nil, 0, err
		}
		if !allowed {
			return nil, 0, ErrSyncForbidden
		}
	}
	// The cursor row serializes writers of one collection.
	var head int64
	err = tx.QueryRowContext(ctx, `INSERT INTO browser_sync_record_cursors(vault_id,collection) VALUES($1,$2)
		ON CONFLICT(vault_id,collection) DO UPDATE SET sequence=browser_sync_record_cursors.sequence
		RETURNING sequence`, vault, collection).Scan(&head)
	if err != nil {
		return nil, 0, err
	}
	if clipboard {
		if head, err = expireClips(ctx, tx, vault, head); err != nil {
			return nil, 0, err
		}
	}
	var live int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM browser_sync_records WHERE vault_id=$1 AND collection=$2 AND ciphertext IS NOT NULL`, vault, collection).Scan(&live); err != nil {
		return nil, 0, err
	}
	results := make([]SyncRecordResult, 0, len(writes))
	for _, w := range writes {
		current := &SyncRecord{Key: w.Key}
		err := tx.QueryRowContext(ctx, `SELECT version,ciphertext,sequence FROM browser_sync_records WHERE vault_id=$1 AND collection=$2 AND record_key=$3 FOR UPDATE`, vault, collection, w.Key).Scan(&current.Version, &current.Ciphertext, &current.Sequence)
		if errors.Is(err, sql.ErrNoRows) {
			current = nil
		} else if err != nil {
			return nil, 0, err
		}
		stored := int64(0)
		if current != nil {
			stored = current.Version
		}
		creating := w.Ciphertext != nil && (current == nil || current.Ciphertext == nil)
		if stored != w.BaseVersion || (creating && live >= syncRecordMaxPerStore) {
			results = append(results, SyncRecordResult{Key: w.Key, Version: stored, Current: current})
			continue
		}
		head++
		_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_records(vault_id,collection,record_key,version,ciphertext,sequence,device_id) VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT(vault_id,collection,record_key) DO UPDATE SET version=excluded.version,ciphertext=excluded.ciphertext,sequence=excluded.sequence,device_id=excluded.device_id,updated_at=now()`,
			vault, collection, w.Key, stored+1, w.Ciphertext, head, device)
		if err != nil {
			return nil, 0, err
		}
		switch {
		case creating:
			live++
		case w.Ciphertext == nil && current != nil && current.Ciphertext != nil:
			live--
		}
		results = append(results, SyncRecordResult{Key: w.Key, Applied: true, Version: stored + 1})
	}
	// Opportunistic compaction: old deletions go, and the floor records the
	// newest one removed so a client behind it re-reads the collection.
	_, err = tx.ExecContext(ctx, `WITH gone AS (
			DELETE FROM browser_sync_records WHERE vault_id=$1 AND collection=$2 AND ciphertext IS NULL AND updated_at < now() - make_interval(days => $3)
			RETURNING sequence)
		UPDATE browser_sync_record_cursors SET sequence=$4, compacted_through=GREATEST(compacted_through, COALESCE((SELECT max(sequence) FROM gone), 0))
		WHERE vault_id=$1 AND collection=$2`, vault, collection, syncRecordTombstoneDays, head)
	if err != nil {
		return nil, 0, err
	}
	if err = notifyRecords(ctx, tx, userID, collection); err != nil {
		return nil, 0, err
	}
	return results, head, cloudusage.Commit(ctx, tx)
}

// MarkBrowserSyncRecordsCapable records that a device keeps bookmarks in the
// cold store. The first time, other devices are told so they can re-check
// whether the shared workspace has retired.
func (db *Store) MarkBrowserSyncRecordsCapable(ctx context.Context, userID, vault, device string) error {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE browser_sync_devices d SET uses_collections=true FROM browser_sync_vaults w
		WHERE w.vault_id=d.vault_id AND w.user_id=$1 AND d.vault_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL AND NOT d.uses_collections`, userID, vault, device)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	if err = notifySync(ctx, tx, userID, vault, "browser-presence"); err != nil {
		return err
	}
	return cloudusage.Commit(ctx, tx)
}

// sharedWorkspaceRetired: every active device keeps bookmarks in the cold store,
// so nothing reads the shared workspace any longer and nothing may write it.
func sharedWorkspaceRetired(ctx context.Context, tx *sql.Tx, vault string) (bool, error) {
	var retired sql.NullBool
	err := tx.QueryRowContext(ctx, `SELECT bool_and(uses_collections) FROM browser_sync_devices WHERE vault_id=$1 AND revoked_at IS NULL`, vault).Scan(&retired)
	return retired.Valid && retired.Bool, err
}

// notifyRecords sends a content-free hint that a collection's cursor moved.
func notifyRecords(ctx context.Context, tx *sql.Tx, userID, collection string) error {
	return notifySync(ctx, tx, userID, collection, "browser-records")
}

// expireClips tombstones clips older than a day, each at a new sequence so
// every device's next pull drops it. It returns the collection's new head.
func expireClips(ctx context.Context, tx *sql.Tx, vault string, head int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT record_key FROM browser_sync_records WHERE vault_id=$1 AND collection=$2 AND ciphertext IS NOT NULL
		AND updated_at <= now() - $3::interval ORDER BY sequence FOR UPDATE`, vault, syncClipboardCollection, syncClipboardLifetime)
	if err != nil {
		return head, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return head, err
		}
		keys = append(keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return head, err
	}
	for _, key := range keys {
		head++
		if _, err := tx.ExecContext(ctx, `UPDATE browser_sync_records SET ciphertext=NULL,version=version+1,sequence=$4,updated_at=now()
			WHERE vault_id=$1 AND collection=$2 AND record_key=$3`, vault, syncClipboardCollection, key, head); err != nil {
			return head, err
		}
	}
	return head, nil
}
