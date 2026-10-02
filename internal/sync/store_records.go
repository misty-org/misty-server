package browsersync

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
)

// Cold-tier collections: rarely changed, endpoint-encrypted records that are
// pulled rather than pushed. Each write is a compare-and-swap on the record's
// version; a loser gets the current row back and rebases.
const (
	syncRecordMaxBytes      = 64 << 10
	syncRecordMaxWrites     = 500
	syncRecordMaxPage       = 1000
	syncRecordMaxPerStore   = 50_000
	syncRecordTombstoneDays = 30
)

var (
	syncRecordCollections = map[string]bool{"bookmarks": true, "tab_groups": true, "history": true}
	syncRecordKeyPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type SyncRecord struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
	// Nil is a deletion.
	Ciphertext []byte `json:"ciphertext"`
	Sequence   int64  `json:"sequence"`
}

type SyncRecordWrite struct {
	Key         string `json:"key"`
	BaseVersion int64  `json:"base_version"`
	Ciphertext  []byte `json:"ciphertext"`
}

type SyncRecordResult struct {
	Key     string `json:"key"`
	Applied bool   `json:"applied"`
	Version int64  `json:"version"`
	// The row a losing write should rebase onto (nil: it does not exist).
	Current *SyncRecord `json:"current,omitempty"`
}

type SyncRecordListing struct {
	Collection string       `json:"collection"`
	Records    []SyncRecord `json:"records"`
	Cursor     int64        `json:"cursor"`
	More       bool         `json:"more"`
	// The client's cursor predates compacted deletions: this page starts a
	// full re-read, and records missing from it once complete were deleted.
	Reset bool `json:"reset"`
}

func validRecordWrite(w SyncRecordWrite) bool {
	return syncRecordKeyPattern.MatchString(w.Key) && w.BaseVersion >= 0 && w.BaseVersion < SyncMaxCounter && len(w.Ciphertext) <= syncRecordMaxBytes
}

// PullBrowserSyncRecords returns a collection's records changed after `since`,
// in sequence order, a page at a time.
func (db *Store) PullBrowserSyncRecords(ctx context.Context, userID, vault, device, collection string, since int64, limit int) (*SyncRecordListing, error) {
	if !syncRecordCollections[collection] || since < 0 {
		return nil, ErrSyncInvalid
	}
	if limit <= 0 || limit > syncRecordMaxPage {
		limit = syncRecordMaxPage
	}
	tx, err := db.readTx(ctx, userID, vault, device)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &SyncRecordListing{Collection: collection, Records: []SyncRecord{}}
	var head, floor int64
	err = tx.QueryRowContext(ctx, `SELECT sequence,compacted_through FROM browser_sync_record_cursors WHERE vault_id=$1 AND collection=$2`, vault, collection).Scan(&head, &floor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if since > head {
		return nil, ErrSyncCursor
	}
	if since > 0 && since < floor {
		out.Reset, since = true, 0
	}
	rows, err := tx.QueryContext(ctx, `SELECT record_key,version,ciphertext,sequence FROM browser_sync_records WHERE vault_id=$1 AND collection=$2 AND sequence>$3 ORDER BY sequence LIMIT $4`, vault, collection, since, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SyncRecord
		if err := rows.Scan(&r.Key, &r.Version, &r.Ciphertext, &r.Sequence); err != nil {
			return nil, err
		}
		out.Records = append(out.Records, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Records) > limit {
		out.Records, out.More = out.Records[:limit], true
	}
	out.Cursor = since
	if n := len(out.Records); n > 0 {
		out.Cursor = out.Records[n-1].Sequence
	} else if !out.More {
		out.Cursor = head
	}
	return out, tx.Commit()
}

// PushBrowserSyncRecords applies each write whose base version matches the
// stored record, and returns the current row for each that lost.
func (db *Store) PushBrowserSyncRecords(ctx context.Context, userID, vault, device, collection string, writes []SyncRecordWrite) ([]SyncRecordResult, int64, error) {
	if !syncRecordCollections[collection] || len(writes) == 0 || len(writes) > syncRecordMaxWrites {
		return nil, 0, ErrSyncInvalid
	}
	for _, w := range writes {
		if !validRecordWrite(w) {
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
	// The cursor row serializes writers of one collection.
	var head int64
	err = tx.QueryRowContext(ctx, `INSERT INTO browser_sync_record_cursors(vault_id,collection) VALUES($1,$2)
		ON CONFLICT(vault_id,collection) DO UPDATE SET sequence=browser_sync_record_cursors.sequence
		RETURNING sequence`, vault, collection).Scan(&head)
	if err != nil {
		return nil, 0, err
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
	return results, head, tx.Commit()
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
	return tx.Commit()
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
