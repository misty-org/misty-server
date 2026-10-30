package browsersync

import (
	"context"

	"github.com/lib/pq"
)

type SyncBlob struct {
	Hash       []byte `json:"hash"`
	Ciphertext []byte `json:"ciphertext"`
}

// PutBrowserSyncBlob stores an encrypted favicon under its keyed hash. The
// server cannot verify the HMAC (it has no key), so a blob is only ever
// shared within its own vault. Unreferenced blobs expire after 60 days.
func (db *Store) PutBrowserSyncBlob(ctx context.Context, userID, vault, device string, blob SyncBlob) error {
	if len(blob.Hash) != 32 || !validCiphertext(blob.Ciphertext, SyncWorkspaceMaxBlobBytes) {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ok bool
	if err = tx.QueryRowContext(ctx, `SELECT true FROM browser_sync_vaults w JOIN browser_sync_devices d USING(vault_id) WHERE w.user_id=$1 AND w.vault_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL AND d.full_sync`, userID, vault, device).Scan(&ok); err != nil {
		return ErrSyncForbidden
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM browser_sync_blobs WHERE vault_id=$1`, vault).Scan(&count); err != nil {
		return err
	}
	if count >= 20000 {
		return ErrSyncInvalid
	}
	// First writer wins; the same keyed hash always names the same content.
	if _, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_blobs(vault_id,blob_hash,ciphertext) VALUES($1,$2,$3) ON CONFLICT(vault_id,blob_hash) DO UPDATE SET last_ref_at=now()`, vault, blob.Hash, blob.Ciphertext); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM browser_sync_blobs WHERE vault_id=$1 AND last_ref_at<now()-interval '60 days'`, vault); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *Store) BrowserSyncBlobs(ctx context.Context, userID, vault, device string, hashes [][]byte) ([]SyncBlob, error) {
	if len(hashes) == 0 || len(hashes) > 64 {
		return nil, ErrSyncInvalid
	}
	for _, h := range hashes {
		if len(h) != 32 {
			return nil, ErrSyncInvalid
		}
	}
	tx, err := db.readTx(ctx, userID, vault, device)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT blob_hash,ciphertext FROM browser_sync_blobs WHERE vault_id=$1 AND blob_hash=ANY($2)`, vault, pq.ByteaArray(hashes))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncBlob{}
	for rows.Next() {
		var b SyncBlob
		if err = rows.Scan(&b.Hash, &b.Ciphertext); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
