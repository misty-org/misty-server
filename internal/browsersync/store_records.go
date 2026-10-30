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

// The shared clipboard (docs/sync/clipboard.md) is a small ring of
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
	return out, tx.Commit()
}
