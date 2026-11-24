package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/misty-org/misty-server/internal/spaces"
)

type CollabMember struct {
	Name          string
	AvatarVersion int64
}

func collabSpaceActiveTx(ctx context.Context, tx *sql.Tx, spaceID string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM spaces WHERE id=$1 AND lifecycle_state='active'`, spaceID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (db *Database) CollabSpaceExists(ctx context.Context, spaceID string) (bool, error) {
	exists := false
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		exists, err = collabSpaceActiveTx(ctx, tx, spaceID)
		return err
	})
	return exists, err
}

func (db *Database) CollabMember(ctx context.Context, spaceID, userID string) (CollabMember, error) {
	var member CollabMember
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		active, err := collabSpaceActiveTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if !active {
			return ErrSpaceNotFound
		}
		err = tx.QueryRowContext(ctx, `SELECT u.username,u.avatar_version FROM space_members m JOIN users u ON u.id=m.user_id
			WHERE m.space_id=$1 AND m.user_id=$2`, spaceID, userID).Scan(&member.Name, &member.AvatarVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceForbidden
		}
		return err
	})
	return member, err
}

func (db *Database) CollabFileAccess(ctx context.Context, spaceID, itemID, userID string) (bool, bool, error) {
	read, write := false, false
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		active, err := collabSpaceActiveTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if !active {
			return ErrSpaceNotFound
		}
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, false)
		if err != nil {
			return err
		}
		if item.Kind != driveKindFile || item.Trashed {
			return ErrDriveNotFound
		}
		read = true
		write, err = hasSpacePermissionTx(ctx, tx, userID, spaceID, spaces.PermissionDriveWrite)
		return err
	})
	return read, write, err
}
