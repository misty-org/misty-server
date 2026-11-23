package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/misty-org/misty-server/internal/spaces"
)

const DriveIgnoreName = ".mistyignore"

var ErrDriveProtected = errors.New("the drive's root .mistyignore stays in place")

func isRootDriveIgnore(parentID *string, name string) bool {
	return parentID == nil && strings.EqualFold(name, DriveIgnoreName)
}

func requireDriveIgnoreEditTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return err
	}
	if access.owner || access.base.Has(spaces.PermissionDrivePurge) {
		return nil
	}
	return ErrLibraryForbidden
}

type DriveIgnoreNeed struct {
	OwnerID string
	Missing bool
}

func (db *Database) DriveRootIgnoreNeed(ctx context.Context, userID, spaceID string) (DriveIgnoreNeed, error) {
	var need DriveIgnoreNeed
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1`, spaceID).Scan(&need.OwnerID); err != nil {
			return err
		}
		var present bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_drive_items
			WHERE space_id=$1 AND parent_id IS NULL AND misty_drive_name_fold(name)=misty_drive_name_fold($2::text)
			AND trashed_at IS NULL AND deleted_at IS NULL)`, spaceID, DriveIgnoreName).Scan(&present); err != nil {
			return err
		}
		need.Missing = !present
		return nil
	})
	return need, err
}
