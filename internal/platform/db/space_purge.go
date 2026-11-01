package db

import (
	"context"
	"database/sql"
)

// The Space purge removes a deleted Space for good once its recovery window
// ends: an owner's delete waits 30 days, a deleted account's Spaces go at
// once. Notes and drawings live in collaboration rooms, so each first gets a
// purge command (the note-control and drawing-control workers deliver them).
// When every room has confirmed, the caller deletes the Space's stored
// objects (SpacePurgeObjectKeys) and then its rows (DeleteSpaceData): the
// Space with everything that cascades from it, and its Library.

// DueSpacePurges lists deleted Spaces whose recovery window has ended.
func (db *Database) DueSpacePurges(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	ids := []string{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM spaces WHERE lifecycle_state='pending_deletion'
			AND permanent_delete_after<=NOW() ORDER BY permanent_delete_after LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

// BeginSpacePurge queues a room purge for every note and drawing in the
// Space that lacks one, and reports how many rooms have not confirmed yet.
func (db *Database) BeginSpacePurge(ctx context.Context, spaceID string) (int, error) {
	waiting := 0
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		notes, err := spacePurgeIDsTx(ctx, tx, `SELECT n.id FROM space_notes n WHERE n.space_id=$1
			AND NOT EXISTS(SELECT 1 FROM space_note_control_outbox o WHERE o.note_id=n.id AND o.command='purge')`, spaceID)
		if err != nil {
			return err
		}
		for _, id := range notes {
			if _, err := tx.ExecContext(ctx, `UPDATE space_notes SET lifecycle_state=$1,acl_version=acl_version+1,updated_at=NOW() WHERE id=$2`, NoteLifecycleDeleting, id); err != nil {
				return err
			}
			if err := enqueueNoteControlTx(ctx, tx, id, "purge", nil); err != nil {
				return err
			}
		}
		drawings, err := spacePurgeIDsTx(ctx, tx, `SELECT d.id FROM space_drawings d WHERE d.space_id=$1
			AND NOT EXISTS(SELECT 1 FROM space_drawing_control_outbox o WHERE o.drawing_id=d.id AND o.command='purge')`, spaceID)
		if err != nil {
			return err
		}
		for _, id := range drawings {
			if _, err := tx.ExecContext(ctx, `UPDATE space_drawings SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=NOW() WHERE id=$1`, id); err != nil {
				return err
			}
			if err := enqueueDrawingControlTx(ctx, tx, id, "purge", nil); err != nil {
				return err
			}
		}
		return tx.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM space_notes n WHERE n.space_id=$1 AND NOT EXISTS(SELECT 1 FROM space_note_control_outbox o
			  WHERE o.note_id=n.id AND o.command='purge' AND o.delivered_at IS NOT NULL))+
			(SELECT count(*) FROM space_drawings d WHERE d.space_id=$1 AND NOT EXISTS(SELECT 1 FROM space_drawing_control_outbox o
			  WHERE o.drawing_id=d.id AND o.command='purge' AND o.delivered_at IS NOT NULL))`, spaceID).Scan(&waiting)
	})
	return waiting, err
}

func spacePurgeIDsTx(ctx context.Context, tx *sql.Tx, query, spaceID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, spaceID)
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

// SpacePurgeObjectKeys lists every stored object of a deleted Space: its
// Library's blobs (files, previews, renditions, exports) and pending uploads.
func (db *Database) SpacePurgeObjectKeys(ctx context.Context, spaceID string) ([]string, error) {
	keys := []string{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var err error
		keys, err = spacePurgeIDsTx(ctx, tx, `SELECT b.r2_object_key FROM library_blobs b JOIN spaces s ON s.security_domain_id=b.security_domain_id
			WHERE s.id=$1 AND s.lifecycle_state='pending_deletion'
			UNION SELECT u.object_key FROM space_library_uploads u JOIN spaces s ON s.id=u.space_id OR s.security_domain_id=u.security_domain_id
			WHERE s.id=$1 AND s.lifecycle_state='pending_deletion'`, spaceID)
		return err
	})
	return keys, err
}

// DeleteSpaceData removes a deleted Space's rows after its objects are gone.
func (db *Database) DeleteSpaceData(ctx context.Context, spaceID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		var domain string
		err := tx.QueryRowContext(ctx, `SELECT security_domain_id FROM spaces WHERE id=$1 AND lifecycle_state='pending_deletion' FOR UPDATE`, spaceID).Scan(&domain)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		// The Space and its domain point at each other; both checks wait for commit.
		if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS security_domains_space_fk, spaces_security_domain_fk DEFERRED`); err != nil {
			return err
		}
		if err := deleteSpaceVaultsTx(ctx, tx, spaceID, ""); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM spaces WHERE id=$1`, spaceID); err != nil {
			return err
		}
		for _, statement := range []string{
			`DELETE FROM library_exports WHERE security_domain_id=$1`,
			`DELETE FROM library_legal_holds WHERE security_domain_id=$1`,
			`DELETE FROM library_recovery_tombstones WHERE security_domain_id=$1`,
			`DELETE FROM library_derivatives WHERE security_domain_id=$1`,
			`DELETE FROM library_processing_jobs WHERE security_domain_id=$1`,
			`DELETE FROM space_library_uploads WHERE security_domain_id=$1`,
			`DELETE FROM library_files WHERE security_domain_id=$1`,
			`DELETE FROM library_blobs WHERE security_domain_id=$1`,
			`DELETE FROM security_domains WHERE id=$1`,
		} {
			if _, err := tx.ExecContext(ctx, statement, domain); err != nil {
				return err
			}
		}
		return nil
	})
}
