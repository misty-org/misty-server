package db

import (
	"context"
	"database/sql"
)

func handleNoteMembershipLossTx(ctx context.Context, tx *sql.Tx, spaceID, _ string) error {
	return revokeNoteAccessForSpaceTx(ctx, tx, spaceID)
}

func revokeNoteAccessForSpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE space_notes SET acl_version=acl_version+1
		 WHERE space_id=$1 AND lifecycle_state IN ($2,$3)`,
		spaceID, NoteLifecycleActive, NoteLifecycleArchived)
	return err
}

func handleNoteMembershipRestoreTx(_ context.Context, _ *sql.Tx, _, _ string) error {
	return nil
}

func (db *Database) PurgeExpiredNotes(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var purged int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		ids, err := queryItemIDsTx(ctx, tx,
			`DELETE FROM space_notes WHERE id IN (
			     SELECT n.id FROM space_notes n
			     WHERE ((n.lifecycle_state=$1 AND n.purge_after<=NOW()) OR n.lifecycle_state=$2)
			       AND NOT EXISTS (
			           SELECT 1 FROM space_note_assets a
			           WHERE a.note_id=n.id AND a.lifecycle_state<>'deleted')
			     LIMIT $3) RETURNING id`,
			NoteLifecycleArchivedCreatorLeft, NoteLifecycleDeleting, limit)
		if err != nil {
			return err
		}
		purged = int64(len(ids))
		return releaseItemContentStorageTx(ctx, tx, ids)
	})
	return purged, err
}

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
