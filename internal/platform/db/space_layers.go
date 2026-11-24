package db

import (
	"context"
	"database/sql"
	"strings"
)

const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

func validVisibility(visibility string) bool {
	return visibility == VisibilityPublic || visibility == VisibilityPrivate
}

var visibilityTables = withSpaceItemVisibility(map[string][3]string{
	"node":  {"space_nodes", "uploader_user_id", "id"},
	"agent": {"space_agents", "owner_user_id", "agent_id"},
})

func withSpaceItemVisibility(others map[string][3]string) map[string][3]string {
	for _, kind := range spaceItemKinds {
		others[kind.Name] = [3]string{kind.Table, kind.OwnerColumn, "id"}
	}
	return others
}

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
		result, err := tx.ExecContext(ctx, `UPDATE `+table[0]+` SET visibility=$1 WHERE space_id=$2 AND `+table[2]+`=$3 AND `+table[1]+`=$4`,
			visibility, spaceID, itemID, userID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		if err := revokeItemConnectionsTx(ctx, tx, kind, itemID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "item.visibility_changed", itemID, map[string]any{"kind": kind, "visibility": visibility})
		return err
	})
}

func purgeSpaceMemberTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_agent_thread_reactions r USING space_agent_threads t
		WHERE r.thread_id=t.id AND t.space_id=$1 AND r.user_id=$2`, spaceID, userID); err != nil {
		return err
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
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_nodes WHERE space_id=$1 AND uploader_user_id=$2 AND visibility='private'`, spaceID, userID); err != nil {
		return err
	}
	return nil
}

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

func revokeItemConnectionsTx(ctx context.Context, tx *sql.Tx, kindName, itemID string) error {
	kind, ok := SpaceItemKindNamed(kindName)
	if !ok || kind.Name != kindName {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE `+kind.Table+` SET acl_version=acl_version+1 WHERE id=$1`, itemID)
	return err
}
