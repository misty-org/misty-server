package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// retireLibraryBlobsIfUnusedTx marks each ready blob deleted when nothing
// live references it and returns the object keys to delete. The blob's
// deduplication lock keeps a concurrent upload from reusing it meanwhile.
func retireLibraryBlobsIfUnusedTx(ctx context.Context, tx *sql.Tx, blobIDs []string) ([]string, error) {
	keys := []string{}
	seen := map[string]bool{}
	for _, blobID := range blobIDs {
		if seen[blobID] {
			continue
		}
		seen[blobID] = true
		var domainID, sha, state string
		var byteSize int64
		err := tx.QueryRowContext(ctx, `SELECT security_domain_id,sha256,byte_size,lifecycle_state FROM library_blobs WHERE id=$1 FOR UPDATE`, blobID).Scan(&domainID, &sha, &byteSize, &state)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if state != "ready" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:blob:"+domainID+":"+sha+fmt.Sprint(byteSize)); err != nil {
			return nil, err
		}
		var key string
		err = tx.QueryRowContext(ctx, `UPDATE library_blobs b SET lifecycle_state='deleted',deleted_at=NOW(),version=version+1,updated_at=NOW()
			WHERE b.id=$1 AND b.lifecycle_state='ready'
			AND NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind='blob' AND h.target_id=b.id)
			AND NOT EXISTS(SELECT 1 FROM library_files f WHERE f.blob_id=b.id AND f.lifecycle_state NOT IN ('purging','deleted'))
			AND NOT EXISTS(SELECT 1 FROM library_derivatives d WHERE d.derivative_blob_id=b.id AND d.lifecycle_state<>'deleted')
			AND NOT EXISTS(SELECT 1 FROM library_exports e WHERE e.export_blob_id=b.id AND e.state<>'deleted')
			AND NOT EXISTS(SELECT 1 FROM space_drive_items di WHERE di.blob_key=b.r2_object_key)
			RETURNING b.r2_object_key`, blobID).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// The finished upload that first wrote this object still names it;
		// retiring it too lets the orphan sweep collect the object if the
		// caller's delete fails.
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='deleted',version=version+1,updated_at=NOW() WHERE object_key=$1 AND state='ready'`, key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}
