package db

import (
	"context"
	"database/sql"
	"errors"
)

var ErrSpaceNotShareable = errors.New("space cannot be shared")

func requireShareableSpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	var kind string
	var isDefault bool
	if err := tx.QueryRowContext(ctx, `SELECT kind,is_default FROM spaces WHERE id=$1`, spaceID).Scan(&kind, &isDefault); errors.Is(err, sql.ErrNoRows) {
		return ErrSpaceNotFound
	} else if err != nil {
		return err
	}
	if kind != "space" || isDefault {
		return ErrSpaceNotShareable
	}
	return nil
}

const sharesSpaceSQL = `EXISTS(SELECT 1 FROM space_members a
	JOIN space_members b ON b.space_id=a.space_id AND b.user_id=$2
	JOIN spaces s ON s.id=a.space_id AND s.lifecycle_state='active' AND s.kind='space' AND NOT s.is_default
	WHERE a.user_id=$1)`

func sharesSpaceTx(ctx context.Context, tx *sql.Tx, userID, otherID string) (bool, error) {
	var shares bool
	err := tx.QueryRowContext(ctx, `SELECT `+sharesSpaceSQL, userID, otherID).Scan(&shares)
	return shares, err
}
