package browsersync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/misty-org/misty-server/internal/platform/transport"
)

// A record: an opaque scope (an HMAC of its profile and collection under the vault root), an
// opaque key, a version, and bytes only clients can open. Writes are a compare-and-swap on the
// version. One sequence per vault orders everything, so one cursor covers all of a person's
// profiles and collections.
const (
	SyncMaxRecordBytes = 256 << 10
	SyncMaxWritesPerOp = 200
	SyncPageRows       = 1000
	SyncPageBytes      = 1 << 20
	// Deleted records are kept this long, so a session that was away hears about them.
	SyncTombstoneLife = 90 * 24 * time.Hour
	// The longest a write may ask to be kept (the shared clipboard's clips).
	SyncMaxRecordLife = 24 * time.Hour
)

var syncOpaqueID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SyncRecordWrite is one change from a client. Deleted is a hint so the server can compact
// tombstones; the sealed bytes say the same, and clients only trust those.
type SyncRecordWrite struct {
	Scope       string `json:"scope"`
	Key         string `json:"key"`
	BaseVersion int64  `json:"base_version"`
	Sealed      []byte `json:"sealed"`
	Deleted     bool   `json:"deleted,omitempty"`
	// ExpiresIn asks the server to drop the record after this many seconds.
	ExpiresIn int64 `json:"expires_in,omitempty"`
}

func (w SyncRecordWrite) valid() bool {
	return syncOpaqueID.MatchString(w.Scope) && syncOpaqueID.MatchString(w.Key) &&
		w.BaseVersion >= 0 && w.BaseVersion < SyncMaxCounter &&
		len(w.Sealed) >= 28 && len(w.Sealed) <= SyncMaxRecordBytes &&
		w.ExpiresIn >= 0 && time.Duration(w.ExpiresIn)*time.Second <= SyncMaxRecordLife
}

// SyncRecord is a record as the server holds it.
type SyncRecord struct {
	Scope   string `json:"scope"`
	Key     string `json:"key"`
	Version int64  `json:"version"`
	Sealed  []byte `json:"sealed"`
}

// SyncRecordResult answers one write. A loser gets the server's copy.
type SyncRecordResult struct {
	Scope   string      `json:"scope"`
	Key     string      `json:"key"`
	Applied bool        `json:"applied"`
	Version int64       `json:"version"`
	Current *SyncRecord `json:"current,omitempty"`
}

// SyncRecordPage is one pull. Reset means the cursor was behind what the server kept: the client
// must treat this listing as starting from nothing.
type SyncRecordPage struct {
	Records []SyncRecord `json:"records"`
	Cursor  int64        `json:"cursor"`
	Head    int64        `json:"head"`
	More    bool         `json:"more"`
	Reset   bool         `json:"reset,omitempty"`
}

// PullRecords returns what changed after a cursor, bounded by rows and bytes, so a page always
// fits in a frame. At least one record is always returned when there is one.
func (db *Store) PullRecords(ctx context.Context, i SyncConnectionIdentity, after int64) (*SyncRecordPage, error) {
	if after < 0 || after > SyncMaxCounter {
		return nil, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	page := &SyncRecordPage{Records: []SyncRecord{}}
	var compacted int64
	err = tx.QueryRowContext(ctx, `SELECT head_sequence,compacted_through FROM sync_vaults WHERE vault_id=$1 AND user_id=$2`, i.VaultID, i.UserID).Scan(&page.Head, &compacted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	if after < compacted {
		page.Reset = true
		after = 0
	}
	rows, err := tx.QueryContext(ctx, `SELECT scope,record_key,version,sealed,sequence FROM sync_records
		WHERE vault_id=$1 AND sequence>$2 AND (expires_at IS NULL OR expires_at>clock_timestamp()) ORDER BY sequence LIMIT $3`, i.VaultID, after, SyncPageRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page.Cursor = after
	bytes, hitLimit := 0, false
	for rows.Next() {
		var record SyncRecord
		var sequence int64
		if err = rows.Scan(&record.Scope, &record.Key, &record.Version, &record.Sealed, &sequence); err != nil {
			return nil, err
		}
		if len(page.Records) > 0 && bytes+len(record.Sealed) > SyncPageBytes {
			hitLimit = true
			break
		}
		bytes += len(record.Sealed)
		page.Records = append(page.Records, record)
		page.Cursor = sequence
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !hitLimit && len(page.Records) < SyncPageRows {
		// Nothing further exists: skipped expired rows don't hold the cursor back.
		page.Cursor = page.Head
	}
	page.More = page.Cursor < page.Head
	return page, nil
}

// PushRecords applies writes in one transaction. Each write stands alone: one that lost its
// compare-and-swap is answered with the server's copy, and the others still apply.
func (db *Store) PushRecords(ctx context.Context, i SyncConnectionIdentity, writes []SyncRecordWrite) ([]SyncRecordResult, error) {
	if len(writes) == 0 || len(writes) > SyncMaxWritesPerOp {
		return nil, ErrSyncInvalid
	}
	var adding int64
	for _, write := range writes {
		if !write.valid() {
			return nil, ErrSyncInvalid
		}
		if !write.Deleted {
			adding += int64(len(write.Sealed))
		}
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var head int64
	// Locking the vault row orders every writer, so sequences never interleave.
	err = tx.QueryRowContext(ctx, `SELECT v.head_sequence FROM sync_vaults v JOIN sync_sessions s USING(vault_id)
		WHERE v.vault_id=$1 AND v.user_id=$2 AND s.session_id=$3 AND s.revoked_at IS NULL FOR UPDATE OF v`, i.VaultID, i.UserID, i.SessionID).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	if err = requireSyncWritable(ctx, tx, i.UserID); err != nil {
		return nil, err
	}
	if adding > 0 {
		if err = requireSyncStorageRoom(ctx, tx, i.UserID, adding); err != nil {
			return nil, err
		}
	}
	results := make([]SyncRecordResult, 0, len(writes))
	for _, write := range writes {
		result := SyncRecordResult{Scope: write.Scope, Key: write.Key}
		var current SyncRecord
		err := tx.QueryRowContext(ctx, `SELECT version,sealed FROM sync_records WHERE vault_id=$1 AND scope=$2 AND record_key=$3 FOR UPDATE`, i.VaultID, write.Scope, write.Key).Scan(&current.Version, &current.Sealed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if current.Version != write.BaseVersion {
			current.Scope, current.Key = write.Scope, write.Key
			result.Version, result.Current = current.Version, &current
			results = append(results, result)
			continue
		}
		head++
		var expires any
		if write.ExpiresIn > 0 {
			expires = time.Now().Add(time.Duration(write.ExpiresIn) * time.Second)
		}
		result.Applied, result.Version = true, write.BaseVersion+1
		if _, err = tx.ExecContext(ctx, `INSERT INTO sync_records(vault_id,scope,record_key,version,sealed,deleted,sequence,session_id,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT(vault_id,scope,record_key) DO UPDATE SET version=excluded.version,sealed=excluded.sealed,deleted=excluded.deleted,sequence=excluded.sequence,session_id=excluded.session_id,expires_at=excluded.expires_at,updated_at=now()`,
			i.VaultID, write.Scope, write.Key, result.Version, write.Sealed, write.Deleted, head, i.SessionID, expires); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sync_vaults SET head_sequence=$1 WHERE vault_id=$2`, head, i.VaultID); err != nil {
		return nil, err
	}
	if err = compactRecords(ctx, tx, i.VaultID, head); err != nil {
		return nil, err
	}
	if err = notifySync(ctx, tx, i.UserID, i.VaultID, syncTopicRecords); err != nil {
		return nil, err
	}
	return results, tx.Commit()
}

// compactRecords drops long-deleted and expired rows. A session behind the floor it raises gets a
// full listing, so it can tell what's gone. Runs inside a push, so it costs nothing while idle.
func compactRecords(ctx context.Context, tx *sql.Tx, vaultID string, head int64) error {
	var dropped sql.NullInt64
	err := tx.QueryRowContext(ctx, `WITH gone AS (DELETE FROM sync_records WHERE vault_id=$1 AND sequence<=$2 AND
		((deleted AND updated_at<now()-$3::interval) OR (expires_at IS NOT NULL AND expires_at<=clock_timestamp())) RETURNING sequence)
		SELECT max(sequence) FROM gone`, vaultID, head, SyncTombstoneLife.String()).Scan(&dropped)
	if err != nil {
		return err
	}
	if dropped.Valid {
		_, err = tx.ExecContext(ctx, `UPDATE sync_vaults SET compacted_through=GREATEST(compacted_through,$1) WHERE vault_id=$2`, dropped.Int64, vaultID)
	}
	return err
}

// SyncScopeUsage is how much one opaque scope holds. Only clients know what a scope names.
type SyncScopeUsage struct {
	Scope   string `json:"scope"`
	Bytes   int64  `json:"bytes"`
	Records int64  `json:"records"`
}

// SyncUsage is what the vault takes up on the server.
type SyncUsage struct {
	Bytes  int64            `json:"bytes"`
	Limit  *int64           `json:"limit_bytes"`
	Scopes []SyncScopeUsage `json:"scopes"`
}

// Usage totals the vault per scope.
func (db *Store) Usage(ctx context.Context, userID string) (*SyncUsage, error) {
	usage := &SyncUsage{Scopes: []SyncScopeUsage{}}
	rows, err := db.Conn.QueryContext(ctx, `SELECT r.scope,COALESCE(sum(octet_length(r.sealed)),0),count(*) FILTER (WHERE NOT r.deleted)
		FROM sync_records r JOIN sync_vaults v USING(vault_id) WHERE v.user_id=$1 GROUP BY r.scope ORDER BY r.scope`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scope SyncScopeUsage
		if err = rows.Scan(&scope.Scope, &scope.Bytes, &scope.Records); err != nil {
			return nil, err
		}
		usage.Bytes += scope.Bytes
		usage.Scopes = append(usage.Scopes, scope)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var limit sql.NullInt64
	err = db.Conn.QueryRowContext(ctx, `SELECT storage_bytes FROM account_controls WHERE user_id=$1`, userID).Scan(&limit)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if limit.Valid {
		usage.Limit = &limit.Int64
	}
	return usage, nil
}

const (
	syncTopicRecords   = "sync-records"
	syncTopicSessions  = "sync-sessions"
	syncTopicVaultKey  = "sync-vault-key"
	syncTopicVaultGone = "sync-vault-gone"
)

// notifySync sends a content-free hint to the account's sockets, delivered at commit.
func notifySync(ctx context.Context, tx *sql.Tx, userID, id, topic string) error {
	hint, err := json.Marshal(transport.AccountEvent{UserID: userID, Topic: topic, ID: id})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('misty_account_events',$1)`, string(hint))
	return err
}
