package db

import (
	"context"
	"database/sql"
	"strings"
)

// Private and public layers (docs/spaces/as-profiles.md#private-and-public).
// A Space item is public or private. A private item opens only for its owner,
// and for the owner's own agents on runs the owner asked for. The RLS policies
// say "public, or owned by the viewer"; queries that run as the service say
// the same with itemViewer, so a forgotten filter in either still can't leak.

const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

type publicLayerOnlyKey struct{}

// WithPublicLayerOnly marks work that may read only the public layer, such
// as a run a teammate asked of someone's agent: the agent acts as its owner,
// but none of the owner's private items open for it.
func WithPublicLayerOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, publicLayerOnlyKey{}, true)
}

// PublicLayerOnly reports whether the work may read only the public layer.
func PublicLayerOnly(ctx context.Context) bool {
	only, _ := ctx.Value(publicLayerOnlyKey{}).(bool)
	return only
}

// itemViewer is whose private items a query may return: the user, or nobody
// when the work is public-only. Queries compare it as
// (visibility='public' OR owner=$viewer).
func itemViewer(ctx context.Context, userID string) string {
	if PublicLayerOnly(ctx) {
		return ""
	}
	return userID
}

func validVisibility(visibility string) bool {
	return visibility == VisibilityPublic || visibility == VisibilityPrivate
}

// visibilityTables maps an item kind to its table, owner column and id column.
var visibilityTables = map[string][3]string{
	"note":         {"space_notes", "creator_user_id", "id"},
	"drawing":      {"space_drawings", "creator_user_id", "id"},
	"library_item": {"space_library_items", "added_by_user_id", "id"},
	"node":         {"space_nodes", "uploader_user_id", "id"},
	"code_project": {"space_code_projects", "owner_user_id", "id"},
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
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "item.visibility_changed", itemID, map[string]any{"kind": kind, "visibility": visibility})
		return err
	})
}

// memoryTreeTables hold one person's memory tree in a Space (phase 7), with
// the column naming its owner.
var memoryTreeTables = [][2]string{
	{"memory_chunks", "owner_user_id"},
	{"memory_backlog", "owner_user_id"},
	{"memory_events", "owner_user_id"},
	{"ai_recaps", "user_id"},
	{"misty_memories", "user_id"},
	{"agent_method_lessons", "user_id"},
	{"agent_method_versions", "user_id"},
	{"agent_methods", "user_id"},
}

// purgeSpaceMemberTx deletes everything of one person's in a Space, in the
// transaction that takes them out of it: their memory rows and chunks, their
// private notes, drawings, files and links, and (through the membership's
// cascades) their code projects and agents' memberships.
// Their public items stay with the Space. Their vault in the Space is the
// sync tree's (phase 1) and goes through its own object deletion.
func purgeSpaceMemberTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	for _, table := range memoryTreeTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table[0]+` WHERE space_id=$1 AND `+table[1]+`=$2`, spaceID, userID); err != nil {
			return err
		}
	}
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
