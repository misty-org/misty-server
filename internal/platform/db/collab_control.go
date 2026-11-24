package db

import (
	"context"
	"database/sql"
)

const (
	CollabControlAccessChanged = "collab.access_changed"
	CollabControlPurged        = "collab.purged"
)

func notifyCollabAccessTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE collab_rooms SET acl_version=acl_version+1 WHERE space_id=$1`, spaceID); err != nil {
		return err
	}
	return notifySpaceControlTx(ctx, tx, map[string]any{"type": CollabControlAccessChanged, "space_id": spaceID})
}

func notifyCollabPurgedTx(ctx context.Context, tx *sql.Tx, spaceID, roomID string) error {
	return notifySpaceControlTx(ctx, tx, map[string]any{"type": CollabControlPurged, "space_id": spaceID, "room_id": roomID})
}
