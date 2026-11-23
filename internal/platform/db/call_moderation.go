package db

import (
	"context"
	"database/sql"
)

func (db *Database) RequireCallModerator(ctx context.Context, actorID, room, targetID string) error {
	spaceID, _, ok := voiceRoomParts(room)
	if !ok {
		return ErrSpaceForbidden
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return requireMemberRemovalTx(ctx, tx, actorID, spaceID, targetID)
	})
}
