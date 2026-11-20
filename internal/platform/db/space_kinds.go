package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrSpaceNotShareable refuses bringing anyone into a Space that is only ever its own people's:
// an account's Personal, or a direct message (whose people change only through /dms).
var ErrSpaceNotShareable = errors.New("space cannot be shared")

// requireShareableSpaceTx refuses invitations, removals and leaving through the Space routes for
// a Personal (is_default) or a direct message (kind 'direct').
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

// sharesSpaceSQL is true when $1 and $2 are both members of one active shared Space: never a
// direct message, and never a Personal, which only ever has its owner.
const sharesSpaceSQL = `EXISTS(SELECT 1 FROM space_members a
	JOIN space_members b ON b.space_id=a.space_id AND b.user_id=$2
	JOIN spaces s ON s.id=a.space_id AND s.lifecycle_state='active' AND s.kind='space' AND NOT s.is_default
	WHERE a.user_id=$1)`

func sharesSpaceTx(ctx context.Context, tx *sql.Tx, userID, otherID string) (bool, error) {
	var shares bool
	err := tx.QueryRowContext(ctx, `SELECT `+sharesSpaceSQL, userID, otherID).Scan(&shares)
	return shares, err
}
