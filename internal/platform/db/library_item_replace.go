package db

import (
	"context"
	"database/sql"
	"errors"
)

// requireLibraryItemVersionTx checks that the caller may edit a ready item
// that is still at ref.Version, and returns its file. lock holds the item row
// until the transaction ends.
func requireLibraryItemVersionTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, ref LibraryItemVersion, lock bool) (string, error) {
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
		return "", err
	}
	if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, ref.ID); err != nil {
		return "", err
	}
	query := `SELECT file_id,version FROM space_library_items WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready'`
	if lock {
		query += ` FOR UPDATE`
	}
	var fileID string
	var version int64
	err := tx.QueryRowContext(ctx, query, ref.ID, spaceID).Scan(&fileID, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryNotFound
	}
	if err != nil {
		return "", err
	}
	if version != ref.Version {
		return "", ErrLibraryConflict
	}
	return fileID, nil
}

// replaceLibraryItemFileTx makes newFileID the content of the item an upload
// replaces, when it replaces one. The item keeps its id, name and details; it
// shows the new file with no edit applied. The old content's bytes and
// previews are released, and the old file and blobs are retired when nothing
// else uses them; their object keys are returned for deletion after commit.
// It returns an empty item id for an upload that makes a new item.
func replaceLibraryItemFileTx(ctx context.Context, tx *sql.Tx, userID, spaceID, uploadID, newFileID string) (string, []string, error) {
	var itemID string
	var itemVersion sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(replace_item_id,''),replace_item_version FROM space_library_uploads WHERE id=$1`, uploadID).Scan(&itemID, &itemVersion); err != nil {
		return "", nil, err
	}
	if itemID == "" {
		return "", nil, nil
	}
	oldFileID, err := requireLibraryItemVersionTx(ctx, tx, userID, spaceID, LibraryItemVersion{ID: itemID, Version: itemVersion.Int64}, true)
	if err != nil {
		return "", nil, err
	}
	var newVersion int64
	if err := tx.QueryRowContext(ctx, `UPDATE space_library_items SET file_id=$1,current_edit_version_id=NULL,version=version+1,updated_at=NOW() WHERE id=$2 RETURNING version`, newFileID, itemID).Scan(&newVersion); err != nil {
		return "", nil, err
	}
	// The item's contribution covered the old file and its previews; the
	// caller records a new one for the new file.
	var released int64
	if err := tx.QueryRowContext(ctx, `WITH released AS (
			UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW()
			WHERE space_id=$1 AND source_kind='library_item' AND source_id=$2 AND state='active'
			RETURNING logical_bytes)
		SELECT COALESCE(sum(logical_bytes),0) FROM released`, spaceID, itemID).Scan(&released); err != nil {
		return "", nil, err
	}
	if released > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
			return "", nil, err
		}
	}
	blobIDs, err := retireLibraryItemDerivativesTx(ctx, tx, `space_library_item_id=$1`, itemID)
	if err != nil {
		return "", nil, err
	}
	if oldFileID != newFileID {
		fileBlobs, err := retireLibraryFileIfUnusedTx(ctx, tx, oldFileID)
		if err != nil {
			return "", nil, err
		}
		blobIDs = append(blobIDs, fileBlobs...)
	}
	keys, err := retireLibraryBlobsIfUnusedTx(ctx, tx, blobIDs)
	if err != nil {
		return "", nil, err
	}
	if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "library.item.updated", itemID, map[string]any{"item_id": itemID, "version": newVersion}); err != nil {
		return "", nil, err
	}
	if err := insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.item.content_replaced", "library_item", itemID, "success", map[string]any{
		"version": newVersion, "released_bytes": released, "physical_objects_deleted": len(keys),
	}); err != nil {
		return "", nil, err
	}
	return itemID, keys, nil
}
