package db

import (
	"context"
	"database/sql"
	"strings"
)

// Private and public layers.
// A Space item is public or private. A private item opens only for its owner.
// The RLS policies say "public, or owned by the viewer"; queries that run as the
// service say the same, so a forgotten filter in either still can't leak.

const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

func validVisibility(visibility string) bool {
	return visibility == VisibilityPublic || visibility == VisibilityPrivate
}

// visibilityTables maps an item kind to its table, owner column and id column.
var visibilityTables = map[string][3]string{
	"note":         {"space_notes", "creator_user_id", "id"},
	"drawing":      {"space_drawings", "creator_user_id", "id"},
	"library_item": {"space_library_items", "added_by_user_id", "id"},
	"node":         {"space_nodes", "uploader_user_id", "id"},
	"agent":        {"space_agents", "owner_user_id", "agent_id"},
}

// SetSpaceItemVisibility flips one item between public and private. Only its
// owner can, and only while they belong to the Space.
func (db *Database) SetSpaceItemVisibility(ctx context.Context, userID, spaceID, kind, itemID, visibility string) error {
	table, ok := visibilityTables[kind]
	itemID = strings.TrimSpace(itemID)
	if !ok || !validVisibility(visibility) || itemID == "" {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		// Table and column names come from the fixed map above.
		result, err := tx.ExecContext(ctx, `UPDATE `+table[0]+` SET visibility=$1 WHERE space_id=$2 AND `+table[2]+`=$3 AND `+table[1]+`=$4`,
			visibility, spaceID, itemID, userID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		// A doc or drawing made private stops being open for the others now: their tickets and
		// live connections end, and only those who may still see it reconnect.
		if err := revokeItemConnectionsTx(ctx, tx, kind, itemID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "item.visibility_changed", itemID, map[string]any{"kind": kind, "visibility": visibility})
		return err
	})
}

// purgeSpaceMemberTx deletes everything of one person's in a Space, in the
// transaction that takes them out of it: their private notes, drawings, files
// and links, and (through the membership's cascades) their agents' listings.
// Their public items stay with the Space.
func purgeSpaceMemberTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	notes, err := privateItemIDsTx(ctx, tx, `SELECT id FROM space_notes WHERE space_id=$1 AND creator_user_id=$2 AND visibility='private' AND lifecycle_state IN ('active','archived','archived_creator_left')`, spaceID, userID)
	if err != nil {
		return err
	}
	for _, noteID := range notes {
		if _, err := tx.ExecContext(ctx, `UPDATE space_notes SET lifecycle_state=$1,acl_version=acl_version+1,updated_at=NOW() WHERE id=$2`, NoteLifecycleDeleting, noteID); err != nil {
			return err
		}
		if err := recordNoteEventTx(ctx, tx, spaceID, userID, "note.deleted", noteID, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_note_assets SET lifecycle_state='deleting',deleted_at=COALESCE(deleted_at,NOW())
			WHERE note_id=$1 AND lifecycle_state IN ('ready','unreferenced')`, noteID); err != nil {
			return err
		}
		if err := enqueueNoteControlTx(ctx, tx, noteID, "purge", nil); err != nil {
			return err
		}
	}
	drawings, err := privateItemIDsTx(ctx, tx, `SELECT id FROM space_drawings WHERE space_id=$1 AND creator_user_id=$2 AND visibility='private' AND lifecycle_state='active'`, spaceID, userID)
	if err != nil {
		return err
	}
	for _, drawingID := range drawings {
		if _, err := tx.ExecContext(ctx, `UPDATE space_drawings SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=NOW() WHERE id=$1`, drawingID); err != nil {
			return err
		}
		if err := recordDrawingEventTx(ctx, tx, spaceID, userID, "drawing.deleted", drawingID); err != nil {
			return err
		}
		if err := enqueueDrawingControlTx(ctx, tx, drawingID, "purge", nil); err != nil {
			return err
		}
	}
	// Library files skip the 30-day trash: the person chose to leave.
	if _, err := tx.ExecContext(ctx, `WITH gone AS (
			UPDATE space_library_items SET lifecycle_state='trash',trashed_at=NOW(),recover_until=NOW(),version=version+1,updated_at=NOW()
			WHERE space_id=$1 AND added_by_user_id=$2 AND visibility='private' AND lifecycle_state='ready' RETURNING id)
		UPDATE space_storage_contributions SET state='recovery',updated_at=NOW()
			WHERE space_id=$1 AND source_kind='library_item' AND source_id IN (SELECT id FROM gone) AND state='active'`, spaceID, userID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM space_nodes WHERE space_id=$1 AND uploader_user_id=$2 AND visibility='private'`, spaceID, userID)
	return err
}

// purgeSpaceMembersTx runs the purge for everyone in a Space being deleted.
// The public layer waits out the Space's recovery window; nothing private does.
func purgeSpaceMembersTx(ctx context.Context, tx *sql.Tx, spaceID string, memberIDs []string) error {
	for _, memberID := range memberIDs {
		if err := purgeSpaceMemberTx(ctx, tx, spaceID, memberID); err != nil {
			return err
		}
	}
	return nil
}

func privateItemIDsTx(ctx context.Context, tx *sql.Tx, query, spaceID, userID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, spaceID, userID)
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

// revokeItemConnectionsTx moves one doc's or drawing's ACL version on and tells its room, which
// drops every connection opened with an older ticket. Other kinds have no live room.
func revokeItemConnectionsTx(ctx context.Context, tx *sql.Tx, kind, itemID string) error {
	var table string
	var enqueue func(context.Context, *sql.Tx, string, string, map[string]any) error
	switch kind {
	case "note":
		table, enqueue = "space_notes", enqueueNoteControlTx
	case "drawing":
		table, enqueue = "space_drawings", enqueueDrawingControlTx
	default:
		return nil
	}
	var aclVersion int64
	// The table name comes from the fixed switch above.
	if err := tx.QueryRowContext(ctx,
		`UPDATE `+table+` SET acl_version=acl_version+1 WHERE id=$1 RETURNING acl_version`,
		itemID).Scan(&aclVersion); err != nil {
		return err
	}
	return enqueue(ctx, tx, itemID, "acl", map[string]any{"acl_version": aclVersion})
}
