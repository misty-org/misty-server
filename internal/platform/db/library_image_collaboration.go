package db

import (
	"context"
	"database/sql"
	"errors"
)

// LibraryImageCollaborationRole is the role a member takes in a library image's edit room:
// "editor" when they may change library items, "viewer" otherwise. It rechecks that the item
// is in the Space, ready, and visible to them, the same as reading it.
func (db *Database) LibraryImageCollaborationRole(ctx context.Context, userID, spaceID, itemID string) (string, error) {
	role := "viewer"
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		var ready bool
		if err := tx.QueryRowContext(ctx, `SELECT lifecycle_state='ready' FROM space_library_items WHERE id=$1 AND space_id=$2`, itemID, spaceID).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return ErrLibraryNotFound
		}
		canEdit, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit)
		if err != nil {
			return err
		}
		if canEdit {
			role = "editor"
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryNotFound
	}
	if err != nil {
		return "", err
	}
	return role, nil
}
