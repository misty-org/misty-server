package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// SpaceNoteProjection is the searchable, non-canonical view emitted by the
// serialized collaboration room after it persists a Yjs snapshot.
type SpaceNoteProjection struct {
	NoteID          string
	Revision        int64
	Title           string
	Markdown        string
	PlainText       string
	OutgoingNoteIDs []string
}

// ApplySpaceNoteProjection accepts only a newer room revision and rebuilds the
// outgoing-link set in the same transaction as title/search projections.
func (db *Database) ApplySpaceNoteProjection(ctx context.Context, projection SpaceNoteProjection) (bool, error) {
	projection.NoteID = strings.TrimSpace(projection.NoteID)
	projection.Title = strings.TrimSpace(projection.Title)
	if projection.NoteID == "" || projection.Revision < 1 || projection.Title == "" ||
		len([]rune(projection.Title)) > 500 || len([]rune(projection.Markdown)) > 100_000 ||
		len([]rune(projection.PlainText)) > 100_000 || len(projection.OutgoingNoteIDs) > 500 {
		return false, ErrSpaceInvalid
	}
	applied := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, creatorUserID string
		err := tx.QueryRowContext(ctx, `UPDATE space_notes
			SET title_projection=$1,markdown_projection=$2,plain_text_projection=$3,
			    collaboration_revision=$4,updated_at=NOW()
			WHERE id=$5 AND lifecycle_state='active' AND collaboration_revision<$4
			RETURNING space_id,creator_user_id`, projection.Title, projection.Markdown,
			projection.PlainText, projection.Revision, projection.NoteID).Scan(&spaceID, &creatorUserID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		applied = true
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_note_links WHERE source_note_id=$1`, projection.NoteID); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, targetID := range projection.OutgoingNoteIDs {
			targetID = strings.TrimSpace(targetID)
			if targetID == "" || targetID == projection.NoteID || seen[targetID] {
				continue
			}
			seen[targetID] = true
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_note_links(source_note_id,target_note_id)
				SELECT $1,n.id FROM space_notes n
				WHERE n.id=$2 AND n.space_id=$3 AND n.lifecycle_state='active'
				ON CONFLICT DO NOTHING`, projection.NoteID, targetID, spaceID); err != nil {
				return err
			}
		}
		return recordNoteEventTx(ctx, tx, spaceID, creatorUserID, "note.projection.updated", projection.NoteID, nil)
	})
	return applied, err
}

type SpaceNoteBacklink struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// SpaceNoteBacklinks returns only source notes the caller can currently see.
func (db *Database) SpaceNoteBacklinks(ctx context.Context, userID, targetNoteID string) ([]SpaceNoteBacklink, error) {
	links := []SpaceNoteBacklink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, targetNoteID)
		if err != nil || !access.CanView {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT source.id,source.title_projection,source.updated_at::text
			FROM space_note_links links
			JOIN space_notes source ON source.id=links.source_note_id
			WHERE links.target_note_id=$1 AND source.lifecycle_state='active'
			  AND (source.audience_kind='space' OR EXISTS(
			      SELECT 1 FROM space_conversation_members cm
			      WHERE cm.conversation_id=source.audience_conversation_id
			        AND cm.actor_kind='person' AND cm.user_id=$2))
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
