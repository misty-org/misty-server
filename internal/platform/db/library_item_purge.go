package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// LibraryItemPurge is one trashed Library item removed for good once its
// recovery window ended. ObjectKeys are the stored objects nothing live uses
// any more. The rows are already retired when the transaction commits, so the
// caller deletes these objects afterwards, and the reconciler's orphan sweep
// removes any it could not.
type LibraryItemPurge struct {
	SpaceID       string
	ItemID        string
	ReleasedBytes int64
	ObjectKeys    []string
}

// PurgeExpiredLibraryItems retires trashed items whose recovery window ended:
// the item, its edit versions and previews, and its file and blobs when no
// other item, attachment or asset shares them. Their storage contributions
// and any reservation they still hold are released.
func (db *Database) PurgeExpiredLibraryItems(ctx context.Context, limit int) ([]LibraryItemPurge, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	out := []LibraryItemPurge{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.space_id,i.file_id FROM space_library_items i
			WHERE i.lifecycle_state='trash' AND i.recover_until<=NOW()
			AND NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind='space_item' AND h.target_id=i.id)
			ORDER BY i.recover_until FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
		if err != nil {
			return err
		}
		type candidate struct{ itemID, spaceID, fileID string }
		candidates := []candidate{}
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.itemID, &item.spaceID, &item.fileID); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range candidates {
			purge, err := purgeLibraryItemTx(ctx, tx, item.spaceID, item.itemID, item.fileID)
			if err != nil {
				return err
			}
			out = append(out, purge)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func purgeLibraryItemTx(ctx context.Context, tx *sql.Tx, spaceID, itemID, fileID string) (LibraryItemPurge, error) {
	purge := LibraryItemPurge{SpaceID: spaceID, ItemID: itemID}
	if _, err := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='deleted',version=version+1,updated_at=NOW() WHERE id=$1`, itemID); err != nil {
		return purge, err
	}

	// Edit versions go with the item. One whose own purge is already running
	// ('purging') is left to finish there.
	editIDs, editBlobs := []string{}, []string{}
	rows, err := tx.QueryContext(ctx, `UPDATE library_item_versions SET lifecycle_state='deleted',deleted_at=COALESCE(deleted_at,NOW())
		WHERE space_library_item_id=$1 AND lifecycle_state IN ('ready','recovery') RETURNING id,COALESCE(rendition_blob_id,'')`, itemID)
	if err != nil {
		return purge, err
	}
	for rows.Next() {
		var editID, blobID string
		if err := rows.Scan(&editID, &blobID); err != nil {
			rows.Close()
			return purge, err
		}
		editIDs = append(editIDs, editID)
		if blobID != "" {
			editBlobs = append(editBlobs, blobID)
		}
	}
	if err := rows.Close(); err != nil {
		return purge, err
	}
	if len(editIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE library_recovery_tombstones SET lifecycle_state='purged',delete_lease_token=NULL,delete_lease_expires_at=NULL,updated_at=NOW()
			WHERE target_kind='edit' AND target_id=ANY($1) AND lifecycle_state='recovery'`, pq.Array(editIDs)); err != nil {
			return purge, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='canceled',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,error_code='item_purged',updated_at=NOW()
			WHERE job_kind='edit' AND target_id=ANY($1) AND state IN ('queued','leased','running')`, pq.Array(editIDs)); err != nil {
			return purge, err
		}
	}

	var reserved int64
	if err := tx.QueryRowContext(ctx, `WITH released AS (
			UPDATE space_rendition_reservations SET state='released',updated_at=NOW()
			WHERE space_id=$1 AND state='active' AND ((source_kind='preview' AND source_id=$2) OR (source_kind='edit' AND source_id=ANY($3)))
			RETURNING reserved_bytes)
		SELECT COALESCE(sum(reserved_bytes),0) FROM released`, spaceID, itemID, pq.Array(editIDs)).Scan(&reserved); err != nil {
		return purge, err
	}
	if err := tx.QueryRowContext(ctx, `WITH released AS (
			UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW()
			WHERE space_id=$1 AND state IN ('active','recovery') AND ((source_kind='library_item' AND source_id=$2) OR (source_kind='edit' AND source_id=ANY($3)))
			RETURNING logical_bytes)
		SELECT COALESCE(sum(logical_bytes),0) FROM released`, spaceID, itemID, pq.Array(editIDs)).Scan(&purge.ReleasedBytes); err != nil {
		return purge, err
	}
	if purge.ReleasedBytes > 0 || reserved > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),reserved_bytes=GREATEST(0,reserved_bytes-$2),version=version+1,updated_at=NOW() WHERE space_id=$3`, purge.ReleasedBytes, reserved, spaceID); err != nil {
			return purge, err
		}
	}

	blobIDs, err := retireLibraryItemDerivativesTx(ctx, tx, `space_library_item_id=$1`, itemID)
	if err != nil {
		return purge, err
	}
	blobIDs = append(blobIDs, editBlobs...)
	fileBlobs, err := retireLibraryFileIfUnusedTx(ctx, tx, fileID)
	if err != nil {
		return purge, err
	}
	blobIDs = append(blobIDs, fileBlobs...)
	purge.ObjectKeys, err = retireLibraryBlobsIfUnusedTx(ctx, tx, blobIDs)
	if err != nil {
		return purge, err
	}

	if _, err := recordSpaceEventTx(ctx, tx, spaceID, "", "library.item.purged", itemID, map[string]any{"item_id": itemID}); err != nil {
		return purge, err
	}
	return purge, insertLibraryAuditTx(ctx, tx, spaceID, "", "", "library.item.purged", "library_item", itemID, "success", map[string]any{
		"released_bytes": purge.ReleasedBytes, "physical_objects_deleted": len(purge.ObjectKeys),
	})
}

// retireLibraryItemDerivativesTx marks derivatives matching condition ($1 is
// arg) deleted and returns the blobs they used.
func retireLibraryItemDerivativesTx(ctx context.Context, tx *sql.Tx, condition, arg string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `UPDATE library_derivatives SET lifecycle_state='deleted',updated_at=NOW()
		WHERE `+condition+` AND lifecycle_state<>'deleted' RETURNING COALESCE(derivative_blob_id,'')`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blobIDs := []string{}
	for rows.Next() {
		var blobID string
		if err := rows.Scan(&blobID); err != nil {
			return nil, err
		}
		if blobID != "" {
			blobIDs = append(blobIDs, blobID)
		}
	}
	return blobIDs, rows.Err()
}

// retireLibraryFileIfUnusedTx marks a file deleted when no live item,
// attachment, note or drawing asset still uses it, along with the file's own
// derivatives, and returns the blobs that may now be unused.
func retireLibraryFileIfUnusedTx(ctx context.Context, tx *sql.Tx, fileID string) ([]string, error) {
	var blobID string
	err := tx.QueryRowContext(ctx, `UPDATE library_files f SET lifecycle_state='deleted',deleted_at=NOW(),version=version+1,updated_at=NOW()
		WHERE f.id=$1 AND f.lifecycle_state='ready'
		AND NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind='file' AND h.target_id=f.id)
		AND NOT EXISTS(SELECT 1 FROM space_library_items i WHERE i.file_id=f.id AND i.lifecycle_state<>'deleted')
		AND NOT EXISTS(SELECT 1 FROM space_message_attachments a WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted')
		AND NOT EXISTS(SELECT 1 FROM space_note_assets a WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted')
		AND NOT EXISTS(SELECT 1 FROM space_drawing_assets a WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted')
		RETURNING f.blob_id`, fileID).Scan(&blobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	derived, err := retireLibraryItemDerivativesTx(ctx, tx, `source_file_id=$1`, fileID)
	if err != nil {
		return nil, err
	}
	return append(derived, blobID), nil
}

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
			AND NOT EXISTS(SELECT 1 FROM library_item_versions v WHERE v.rendition_blob_id=b.id AND v.lifecycle_state<>'deleted')
			AND NOT EXISTS(SELECT 1 FROM library_derivatives d WHERE d.derivative_blob_id=b.id AND d.lifecycle_state<>'deleted')
			AND NOT EXISTS(SELECT 1 FROM library_exports e WHERE e.export_blob_id=b.id AND e.state<>'deleted')
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
