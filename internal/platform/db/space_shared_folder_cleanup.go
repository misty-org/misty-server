package db

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// Giving up a shared folder's uploads, bytes and blobs: what a commit
// replaces and what deleting a folder removes.

// releaseSharedFolderUploadsTx gives up pending folder uploads: their
// reservations free at once and their objects (returned) can be deleted.
func releaseSharedFolderUploadsTx(ctx context.Context, tx *sql.Tx, spaceID string, uploads []SharedFolderPendingUpload) ([]string, error) {
	if len(uploads) == 0 {
		return nil, nil
	}
	ids, keys := make([]string, len(uploads)), make([]string, len(uploads))
	for i, upload := range uploads {
		ids[i], keys[i] = upload.ID, upload.ObjectKey
	}
	var released int64
	if err := tx.QueryRowContext(ctx, `WITH released AS (
		UPDATE space_upload_reservations SET state='released',updated_at=NOW() WHERE upload_id=ANY($1) AND state='active' RETURNING reserved_bytes)
		SELECT COALESCE(sum(reserved_bytes),0) FROM released`, pq.Array(ids)).Scan(&released); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
		return nil, err
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='rejected',error_code='superseded',version=version+1,updated_at=NOW() WHERE id=ANY($1)`, pq.Array(ids))
	return keys, err
}

// releaseSharedFolderContributionsTx stops a folder's bytes counting against
// anyone and returns how many bytes it freed.
func releaseSharedFolderContributionsTx(ctx context.Context, tx *sql.Tx, spaceID, folderID string) (int64, error) {
	var released int64
	if err := tx.QueryRowContext(ctx, `WITH released AS (
		UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW()
		WHERE space_id=$1 AND source_kind='shared_folder' AND source_id=$2 AND state IN ('active','recovery') RETURNING logical_bytes)
		SELECT COALESCE(sum(logical_bytes),0) FROM released`, spaceID, folderID).Scan(&released); err != nil {
		return 0, err
	}
	return released, nil
}

func sharedFolderBlobKeysTx(ctx context.Context, tx *sql.Tx, folderID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT blob_key FROM space_shared_folder_files WHERE folder_id=$1 AND blob_key IS NOT NULL`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// DeleteSharedFolder removes a folder: its manifest goes, its bytes stop
// counting, and its pending uploads are given up. It returns the objects to
// delete now and the blob keys for RetireSharedFolderBlobs.
func (db *Database) DeleteSharedFolder(ctx context.Context, userID, spaceID, folderID string) (deleteKeys, retireKeys []string, err error) {
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryUpload); err != nil {
			return err
		}
		if _, err := sharedFolderTx(ctx, tx, spaceID, folderID, true); err != nil {
			return err
		}
		var err error
		if retireKeys, err = sharedFolderBlobKeysTx(ctx, tx, folderID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_shared_folder_files WHERE folder_id=$1`, folderID); err != nil {
			return err
		}
		released, err := releaseSharedFolderContributionsTx(ctx, tx, spaceID, folderID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,object_key,client_sha256,requested_byte_size,state FROM space_library_uploads
			WHERE shared_folder_id=$1 AND state IN ('initiated','uploading','uploaded_unverified') FOR UPDATE`, folderID)
		if err != nil {
			return err
		}
		pending := []SharedFolderPendingUpload{}
		for rows.Next() {
			var upload SharedFolderPendingUpload
			if err := rows.Scan(&upload.ID, &upload.ObjectKey, &upload.SHA256, &upload.ByteSize, &upload.State); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, upload)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if deleteKeys, err = releaseSharedFolderUploadsTx(ctx, tx, spaceID, pending); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_shared_folders SET deleted_at=NOW(),byte_size=0,file_count=0 WHERE id=$1`, folderID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "folder.deleted", folderID, map[string]any{"folder_id": folderID}); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "folder.deleted", "shared_folder", folderID, "success", map[string]any{"released_bytes": released})
	})
	return deleteKeys, retireKeys, err
}

// RetireSharedFolderBlobs retires the blobs a folder stopped naming when
// nothing else uses them, and returns their objects for deletion. It runs
// after the folder's change commits, a few blobs per transaction, since each
// blob takes its deduplication lock.
func (db *Database) RetireSharedFolderBlobs(ctx context.Context, keys []string) ([]string, error) {
	retired := []string{}
	for start := 0; start < len(keys); start += sharedFolderRetireBatch {
		batch := keys[start:min(start+sharedFolderRetireBatch, len(keys))]
		var gone []string
		err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM library_blobs WHERE r2_object_key=ANY($1) ORDER BY id`, pq.Array(batch))
			if err != nil {
				return err
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			gone, err = retireLibraryBlobsIfUnusedTx(ctx, tx, ids)
			return err
		})
		if err != nil {
			return retired, err
		}
		retired = append(retired, gone...)
	}
	return retired, nil
}
