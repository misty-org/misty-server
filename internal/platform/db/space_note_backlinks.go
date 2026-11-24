package db

import (
	"context"
	"database/sql"
)

type SpaceNoteBacklink struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

func (db *Database) SpaceNoteBacklinks(ctx context.Context, userID, targetNoteID string) ([]SpaceNoteBacklink, error) {
	links := []SpaceNoteBacklink{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, targetNoteID)
		if err != nil || !access.CanView {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT source.id,source.title_projection,source.updated_at::text
			FROM space_note_links links
			JOIN space_notes source ON source.id=links.source_note_id
			WHERE links.target_note_id=$1 AND source.lifecycle_state='active'
			  AND (source.audience_kind='space' OR public.misty_can_read_space_conversation(source.audience_conversation_id,$2))
			ORDER BY source.updated_at DESC`, targetNoteID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var link SpaceNoteBacklink
			if err := rows.Scan(&link.ID, &link.Title, &link.UpdatedAt); err != nil {
				return err
			}
			links = append(links, link)
		}
		return rows.Err()
	})
	return links, err
}
