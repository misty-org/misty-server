package db

import (
	"context"
	"database/sql"
	"time"
)

// NoteArchiveWindow is retained for legacy archived_creator_left rows created
// before native Space notes became membership-wide.
const NoteArchiveWindow = 30 * 24 * time.Hour

// handleNoteMembershipLossTx runs inside the same transaction that removes a
// user from a Space, so a note can never be left reachable by someone who is no
// longer a member.
//
// Notes belong to the Space, so a creator leaving does not archive or remove
// them. Only the departing member's own per-note preferences are cleared.
func handleNoteMembershipLossTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	// Notes belong to the Space, so a departing member takes nothing with them.
	// Only their own per-note UI state is removed.
	_, err := tx.ExecContext(ctx,
		`DELETE FROM space_note_preferences pref USING space_notes n
		 WHERE pref.note_id=n.id AND n.space_id=$1 AND pref.user_id=$2`, spaceID, userID)
	return err
}

// handleNoteMembershipRestoreTx runs inside the transaction that re-adds a user
// to a Space.
//
// Space notes never leave with their creator, so membership restoration needs
// no note-specific work.
func handleNoteMembershipRestoreTx(_ context.Context, _ *sql.Tx, _, _ string) error {
	return nil
}

// PurgeNotesForDeletedAccount preserves collaborative Space notes by assigning
// their destructive controls to the current Space owner.
//
// Misty has no account-deletion flow yet. When one is built it must call this
// in the same transaction that removes or anonymizes the user, before the row
// goes away — the creator id is what identifies the notes to purge.
func (db *Database) PurgeNotesForDeletedAccount(ctx context.Context, userID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return handleNoteAccountDeletionTx(ctx, tx, userID)
	})
}

func handleNoteAccountDeletionTx(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE space_notes n
		SET creator_user_id=s.owner_user_id,updated_at=NOW()
		FROM spaces s
		WHERE n.space_id=s.id AND n.creator_user_id=$1 AND s.owner_user_id<>$1`, userID)
	return err
}

func scanNoteIDs(rows *sql.Rows, queryErr error) ([]string, error) {
	if queryErr != nil {
		return nil, queryErr
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

// PurgeExpiredNotes deletes archived notes whose retention window has passed,
// plus notes already marked deleting whose room purge has been delivered.
//
// Batches are bounded and every step is safe to retry: the worker may be
// interrupted at any point and will simply pick the remainder up next tick.
func (db *Database) PurgeExpiredNotes(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var purged int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// Only notes whose purge command actually reached the collaboration
		// service are removed. Deleting the row first would strand the Durable
		// Object with no record of what to clean up.
		result, err := tx.ExecContext(ctx,
			`DELETE FROM space_notes WHERE id IN (
			     SELECT n.id FROM space_notes n
			     WHERE (n.lifecycle_state=$1 AND n.purge_after<=NOW())
			        OR (n.lifecycle_state=$2 AND EXISTS(
			            SELECT 1 FROM space_note_control_outbox o
			            WHERE o.note_id=n.id AND o.command='purge' AND o.delivered_at IS NOT NULL))
			     LIMIT $3)`,
			NoteLifecycleArchivedCreatorLeft, NoteLifecycleDeleting, limit)
		if err != nil {
			return err
		}
		purged, _ = result.RowsAffected()
		return nil
	})
	return purged, err
}

// NoteControlCommand is one queued instruction for the collaboration service.
type NoteControlCommand struct {
	ID       string
	NoteID   string
	Command  string
	Payload  []byte
	Attempts int
}

// PendingNoteControlCommands claims a bounded batch of undelivered commands and
// pushes their next attempt out, so two workers cannot pick up the same command.
func (db *Database) PendingNoteControlCommands(ctx context.Context, limit int) ([]NoteControlCommand, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	commands := []NoteControlCommand{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`UPDATE space_note_control_outbox SET attempts=attempts+1,
			        next_attempt_at=NOW()+(LEAST(attempts+1,6)*INTERVAL '10 seconds')
			 WHERE id IN (
			     SELECT id FROM space_note_control_outbox
			     WHERE delivered_at IS NULL AND next_attempt_at<=NOW()
			     ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT $1)
			 RETURNING id,note_id,command,payload,attempts`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var command NoteControlCommand
			if err := rows.Scan(&command.ID, &command.NoteID, &command.Command, &command.Payload, &command.Attempts); err != nil {
				return err
			}
			commands = append(commands, command)
		}
		return rows.Err()
	})
	return commands, err
}

// MarkNoteControlDelivered records a successful delivery. It is idempotent.
func (db *Database) MarkNoteControlDelivered(ctx context.Context, commandID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_note_control_outbox SET delivered_at=NOW(),last_error=''
			 WHERE id=$1 AND delivered_at IS NULL`, commandID)
		return err
	})
}

// MarkNoteControlFailed records why a delivery failed so the backlog is
// diagnosable. The row stays pending and is retried.
func (db *Database) MarkNoteControlFailed(ctx context.Context, commandID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_note_control_outbox SET last_error=$1 WHERE id=$2 AND delivered_at IS NULL`,
			reason, commandID)
		return err
	})
}

// NoteControlBacklog reports undelivered commands that are already due, for
// readiness metrics. A small transient backlog is normal.
func (db *Database) NoteControlBacklog(ctx context.Context) (int64, error) {
	var backlog int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM space_note_control_outbox WHERE delivered_at IS NULL AND next_attempt_at<=NOW()`).
			Scan(&backlog)
	})
	return backlog, err
}

// ExpiredNoteAssets returns object keys for assets that are no longer
// referenced and are past their safety window, so the caller can delete the R2
// objects before the rows go away.
func (db *Database) ExpiredNoteAssets(ctx context.Context, safetyWindow time.Duration, limit int) ([]ExpiredLibraryUpload, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if safetyWindow < time.Hour {
		safetyWindow = 24 * time.Hour
	}
	assets := []ExpiredLibraryUpload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT a.id,b.r2_object_key FROM space_note_assets a
			 JOIN library_files f ON f.id=a.file_id
			 JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.lifecycle_state IN ('unreferenced','deleting')
			   AND a.deleted_at IS NOT NULL AND a.deleted_at<=NOW()-$1::interval
			 LIMIT $2`, safetyWindow.String(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset ExpiredLibraryUpload
			if err := rows.Scan(&asset.ID, &asset.ObjectKey); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}

// MarkNoteAssetDeleted finalizes one asset after its object is gone.
func (db *Database) MarkNoteAssetDeleted(ctx context.Context, assetID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_note_assets SET lifecycle_state='deleted',deleted_at=NOW() WHERE id=$1`, assetID)
		return err
	})
}
