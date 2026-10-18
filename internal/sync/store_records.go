package browsersync

import (
	"context"
	"database/sql"
	"errors"
	"github.com/kannachi323/misty/server/internal/cloudusage"
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

// The shared clipboard (docs/design/clipboard/BRIEF.md) is a small ring of
// sealed clips in its own collection: a few larger records that expire after
// a day. Only devices whose signed policy turns the clipboard on reach it.
const (
	syncClipboardCollection = "clipboard"
	syncClipboardMaxBytes   = 256 << 10
	syncClipboardMaxPage    = 8
	syncClipboardLifetime   = "24 hours"
)

var (
	// Space data travels in these too: extensions, site permissions, search
	// engines and bangs, and the downloads list. The server sees only names.
	syncRecordCollections = map[string]bool{
		"bookmarks": true, "tab_groups": true, "history": true, "extension_sync": true, "passwords": true, "space_settings": true,
		"extensions": true, "site_permissions": true, "search": true, "downloads": true,
		syncClipboardCollection: true,
	}
	syncRecordKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func recordMaxBytes(collection string) int {
	if collection == syncClipboardCollection {
		return syncClipboardMaxBytes
	}
	return syncRecordMaxBytes
}

// clipboardAllowed reports whether this sync device belongs to an admitted
// device whose latest signed policy turns the shared clipboard on.
func clipboardAllowed(ctx context.Context, tx *sql.Tx, userID, device string) (bool, error) {
	// Device tables enforce row-level security per user; scope this
	// transaction to the device's own account.
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_user_id',$1,true)`, userID); err != nil {
		return false, err
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices t JOIN device_policies p ON p.device_id=t.id AND p.user_id=t.user_id
		WHERE t.user_id=$1 AND t.sync_device_id::text=$2 AND t.admission_state='admitted' AND p.clipboard)`, userID, device).Scan(&allowed)
	return allowed, err
}

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

func validRecordWrite(collection string, w SyncRecordWrite) bool {
	return syncRecordKeyPattern.MatchString(w.Key) && w.BaseVersion >= 0 && w.BaseVersion < SyncMaxCounter && len(w.Ciphertext) <= recordMaxBytes(collection)
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
	// Large clips arrive a few at a time so each frame stays small.
	clipboard := collection == syncClipboardCollection
	if clipboard && limit > syncClipboardMaxPage {
		limit = syncClipboardMaxPage
	}
	tx, err := db.readTx(ctx, userID, vault, device)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if clipboard {
		allowed, err := clipboardAllowed(ctx, tx, userID, device)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrSyncForbidden
		}
	}
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
	// Expired clips are never served, even before a write tombstones them.
	rows, err := tx.QueryContext(ctx, `SELECT record_key,version,ciphertext,sequence FROM browser_sync_records WHERE vault_id=$1 AND collection=$2 AND sequence>$3
		AND ($5=false OR ciphertext IS NULL OR updated_at > now() - $6::interval) ORDER BY sequence LIMIT $4`, vault, collection, since, limit+1, clipboard, syncClipboardLifetime)
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
	return out, cloudusage.Commit(ctx, tx)
}

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
