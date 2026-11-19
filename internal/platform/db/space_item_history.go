package db

import (
	"context"
	"database/sql"
	"errors"
)

// A made item's earlier versions live in its collaboration room, which keeps snapshots as it is
// edited. Reading them needs what reading the item needs, and restoring one what editing it
// needs; the API checks both here before asking the room.

// SpaceItemAccess resolves one caller's access to an item of any registered kind in one Space.
// An item in another Space, missing, or not visible to the caller is ErrSpaceNotFound.
func (db *Database) SpaceItemAccess(ctx context.Context, kindName, userID, spaceID, itemID string) (MadeItemAccess, SpaceItemLimits, error) {
	kind, ok := SpaceItemKindNamed(kindName)
	if !ok {
		return MadeItemAccess{}, SpaceItemLimits{}, ErrSpaceNotFound
	}
	var access MadeItemAccess
	var limits SpaceItemLimits
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var itemSpace string
		// The table name comes from the registry.
		err := tx.QueryRowContext(ctx, `SELECT space_id FROM `+kind.Table+` WHERE id=$1`, itemID).Scan(&itemSpace)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && itemSpace != spaceID) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		switch kind.Name {
		case SpaceItemNote:
			noteAccess, err := noteAccessForTx(ctx, tx, userID, itemID)
			if err != nil {
				return err
			}
			access = MadeItemAccess{CanView: noteAccess.CanView, CanEdit: noteAccess.CanEdit,
				CanDelete: noteAccess.CanDelete, Role: noteAccess.Role}
		case SpaceItemDrawing:
			access, err = drawingAccessForTx(ctx, tx, userID, itemID)
		default:
			access, err = madeItemAccessForTx(ctx, tx, kind, userID, itemID)
		}
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrSpaceNotFound
		}
		limits, err = spaceItemLimitsTx(ctx, tx, spaceID)
		return err
	})
	if err != nil {
		return MadeItemAccess{}, SpaceItemLimits{}, err
	}
	return access, limits, nil
}
