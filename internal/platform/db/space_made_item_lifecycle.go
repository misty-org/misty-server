package db

import (
	"context"
	"database/sql"
)

// The rest of a deck's or workbook's life after its record: purging a member's private ones,
// revoking room access when someone leaves, purging a Space, the room control queue, and the
// Space level's size limit.

// purgeMadeItemsOfMemberTx deletes one person's private decks and workbooks in a Space, as
// purgeSpaceMemberTx does their private notes and drawings.
func purgeMadeItemsOfMemberTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	for _, kind := range sharedSpaceItemKinds() {
		ids, err := privateItemIDsTx(ctx, tx, `UPDATE `+kind.Table+`
			SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=NOW()
			WHERE space_id=$1 AND creator_user_id=$2 AND visibility='private' AND lifecycle_state='active'
			RETURNING id`, spaceID, userID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := deleteMadeItemTx(ctx, tx, kind, spaceID, userID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// revokeMadeItemAccessForSpaceTx runs with every membership loss, as revokeDrawingAccessForSpaceTx
// does for drawings: every active deck's and workbook's room drops the sockets minted before, and
// the members still there reconnect with fresh tickets.
func revokeMadeItemAccessForSpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	for _, kind := range sharedSpaceItemKinds() {
		rows, err := tx.QueryContext(ctx,
			`UPDATE `+kind.Table+` SET acl_version=acl_version+1,updated_at=NOW()
			 WHERE space_id=$1 AND lifecycle_state='active' RETURNING id,acl_version`, spaceID)
		if err != nil {
			return err
		}
		type revision struct {
			id         string
			aclVersion int64
		}
		revisions := []revision{}
		for rows.Next() {
			var item revision
			if err := rows.Scan(&item.id, &item.aclVersion); err != nil {
				rows.Close()
				return err
			}
			revisions = append(revisions, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range revisions {
			if err := kind.enqueueControlTx(ctx, tx, item.id, "acl",
				map[string]any{"acl_version": item.aclVersion}); err != nil {
				return err
			}
		}
	}
	return nil
}

// beginMadeItemSpacePurgeTx queues a room purge for every deck and workbook of a purging Space
// that lacks one, and reports how many rooms have not confirmed theirs.
func beginMadeItemSpacePurgeTx(ctx context.Context, tx *sql.Tx, spaceID string) (int, error) {
	waiting := 0
	for _, kind := range sharedSpaceItemKinds() {
		ids, err := spacePurgeIDsTx(ctx, tx, `SELECT m.id FROM `+kind.Table+` m WHERE m.space_id=$1
			AND NOT EXISTS(SELECT 1 FROM `+kind.ControlTable+` o WHERE o.`+kind.ControlColumn+`=m.id AND o.command='purge')`, spaceID)
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE `+kind.Table+` SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=NOW() WHERE id=$1`, id); err != nil {
				return 0, err
			}
			if err := kind.enqueueControlTx(ctx, tx, id, "purge", nil); err != nil {
				return 0, err
			}
		}
		var unconfirmed int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+kind.Table+` m WHERE m.space_id=$1
			AND NOT EXISTS(SELECT 1 FROM `+kind.ControlTable+` o
			  WHERE o.`+kind.ControlColumn+`=m.id AND o.command='purge' AND o.delivered_at IS NOT NULL)`, spaceID).Scan(&unconfirmed); err != nil {
			return 0, err
		}
		waiting += unconfirmed
	}
	return waiting, nil
}

// MadeItemControlCommand is one retryable instruction for a deck's or workbook's room.
type MadeItemControlCommand struct {
	ID       string
	ItemID   string
	Command  string
	Payload  []byte
	Attempts int
}

// PendingMadeItemControlCommands claims a bounded batch of one kind's commands, moving each retry
// deadline forward so concurrent workers can't deliver the same row.
func (db *Database) PendingMadeItemControlCommands(ctx context.Context, kindName string, limit int) ([]MadeItemControlCommand, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	commands := []MadeItemControlCommand{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`UPDATE `+kind.ControlTable+`
			 SET attempts=attempts+1,
			     next_attempt_at=NOW()+(LEAST(attempts+1,6)*INTERVAL '10 seconds')
			 WHERE id IN (
			     SELECT id FROM `+kind.ControlTable+`
			     WHERE delivered_at IS NULL AND next_attempt_at<=NOW()
			     ORDER BY next_attempt_at
			     FOR UPDATE SKIP LOCKED LIMIT $1)
			 RETURNING id,`+kind.ControlColumn+`,command,payload,attempts`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var command MadeItemControlCommand
			if err := rows.Scan(&command.ID, &command.ItemID, &command.Command, &command.Payload, &command.Attempts); err != nil {
				return err
			}
			commands = append(commands, command)
		}
		return rows.Err()
	})
	return commands, err
}

// MarkMadeItemControlDelivered records that the room carried out the command.
func (db *Database) MarkMadeItemControlDelivered(ctx context.Context, kindName, commandID string) error {
	return db.settleMadeItemControl(ctx, kindName, commandID, "", true)
}

// AbandonMadeItemControlCommand settles a command the room refused for good, such as a title
// change reaching a room already purged. It is not retried.
func (db *Database) AbandonMadeItemControlCommand(ctx context.Context, kindName, commandID, reason string) error {
	return db.settleMadeItemControl(ctx, kindName, commandID, reason, true)
}

// MarkMadeItemControlFailed keeps the command queued with the reason it failed.
func (db *Database) MarkMadeItemControlFailed(ctx context.Context, kindName, commandID, reason string) error {
	return db.settleMadeItemControl(ctx, kindName, commandID, reason, false)
}

func (db *Database) settleMadeItemControl(ctx context.Context, kindName, commandID, reason string, settled bool) error {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return err
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		query := `UPDATE ` + kind.ControlTable + ` SET last_error=$1 WHERE id=$2 AND delivered_at IS NULL`
		if settled {
			query = `UPDATE ` + kind.ControlTable + ` SET delivered_at=NOW(),last_error=$1 WHERE id=$2 AND delivered_at IS NULL`
		}
		_, err := tx.ExecContext(ctx, query, reason, commandID)
		return err
	})
}

// PurgeDeletedMadeItems removes one kind's rows once their rooms have confirmed their content is
// gone, and stops counting what they stored.
func (db *Database) PurgeDeletedMadeItems(ctx context.Context, kindName string, limit int) (int64, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return 0, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	// A deck's images are cleaned up first, after their safety window, so the row (and its
	// assets' cascade) never strands an image's file or its storage.
	assetsGone := ""
	if kind.AssetTable != "" {
		assetsGone = ` AND NOT EXISTS(SELECT 1 FROM ` + kind.AssetTable + ` a
			      WHERE a.` + kind.ControlColumn + `=m.id AND a.lifecycle_state<>'deleted')`
	}
	var purged int64
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		ids, err := queryItemIDsTx(ctx, tx, `DELETE FROM `+kind.Table+` WHERE id IN (
			SELECT m.id FROM `+kind.Table+` m
			WHERE m.lifecycle_state='deleting'
			  AND EXISTS(SELECT 1 FROM `+kind.ControlTable+` o
			      WHERE o.`+kind.ControlColumn+`=m.id AND o.command='purge' AND o.delivered_at IS NOT NULL)`+assetsGone+`
			LIMIT $1) RETURNING id`, limit)
		if err != nil {
			return err
		}
		purged = int64(len(ids))
		return releaseItemContentStorageTx(ctx, tx, ids)
	})
	return purged, err
}

// madeItemCountLimitTx is the Space level's limit on one kind's size, or nil when unlimited.
func madeItemCountLimitTx(ctx context.Context, tx *sql.Tx, kind SpaceItemKind, spaceID string) (*int64, error) {
	limits, err := spaceItemLimitsTx(ctx, tx, spaceID)
	if err != nil {
		return nil, err
	}
	return limits.countLimit(kind), nil
}

// queryItemIDsTx runs a query whose rows are single ids.
func queryItemIDsTx(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
