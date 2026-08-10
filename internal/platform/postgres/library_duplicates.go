package db

import (
	"context"
	"database/sql"
)

func (db *Database) RecordLibraryDuplicate(ctx context.Context, userID, spaceID, sourceItemID, destinationItemID string, logicalBytes int64) error {
	if sourceItemID == "" || destinationItemID == "" || sourceItemID == destinationItemID || logicalBytes < 1 {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items source JOIN space_library_items destination ON destination.space_id=source.space_id WHERE source.id=$1 AND destination.id=$2 AND source.space_id=$3 AND source.lifecycle_state='ready' AND destination.lifecycle_state='ready')`, sourceItemID, destinationItemID, spaceID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrLibraryNotFound
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.item.duplicated", "library_item", destinationItemID, "success", map[string]any{"source_item_id": sourceItemID, "logical_bytes": logicalBytes})
	})
}
