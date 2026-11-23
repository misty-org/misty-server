package db

import (
	"context"
	"database/sql"
	"errors"
)

type MadeItemAccess struct {
	CanView   bool
	CanEdit   bool
	CanDelete bool
	Role      string
}

func (db *Database) SpaceItemAccess(ctx context.Context, kindName, userID, spaceID, itemID string) (MadeItemAccess, SpaceItemLimits, error) {
	kind, ok := SpaceItemKindNamed(kindName)
	if !ok {
		return MadeItemAccess{}, SpaceItemLimits{}, ErrSpaceNotFound
	}
	var access MadeItemAccess
	var limits SpaceItemLimits
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var itemSpace string
		err := tx.QueryRowContext(ctx, `SELECT space_id FROM `+kind.Table+` WHERE id=$1`, itemID).Scan(&itemSpace)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && itemSpace != spaceID) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if kind.Name != SpaceItemNote {
			return ErrSpaceNotFound
		}
		noteAccess, err := noteAccessForTx(ctx, tx, userID, itemID)
		if err != nil {
			return err
		}
		access = MadeItemAccess{CanView: noteAccess.CanView, CanEdit: noteAccess.CanEdit,
			CanDelete: noteAccess.CanDelete, Role: noteAccess.Role}
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
