package db

import (
	"context"
	"database/sql"
)

// handleNoteMembershipLossTx runs inside the same transaction that removes a
// user from a Space, so a note can never be left reachable by someone who is no
// longer a member: every open connection to the Space's notes is cut, and the
// members who are still there reconnect with fresh tickets.
//
// Notes belong to the Space, so a creator leaving does not archive or remove
// them. Journal has no per-member note state to clean up.
func handleNoteMembershipLossTx(ctx context.Context, tx *sql.Tx, spaceID, _ string) error {
	return revokeNoteAccessForSpaceTx(ctx, tx, spaceID)
}

// revokeNoteAccessForSpaceTx invalidates every outstanding ticket and socket for
// a Space's notes, as revokeDrawingAccessForSpaceTx does for its drawings.
func revokeNoteAccessForSpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	rows, err := tx.QueryContext(ctx,
		`UPDATE space_notes SET acl_version=acl_version+1
		 WHERE space_id=$1 AND lifecycle_state IN ($2,$3)
		 RETURNING id,acl_version`,
		spaceID, NoteLifecycleActive, NoteLifecycleArchived)
	if err != nil {
		return err
	}
	type revision struct {
		noteID     string
		aclVersion int64
	}
	revisions := []revision{}
	for rows.Next() {
		var item revision
		if err := rows.Scan(&item.noteID, &item.aclVersion); err != nil {
			rows.Close()
			return err
		}
		revisions = append(revisions, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range revisions {
		if err := enqueueNoteControlTx(ctx, tx, item.noteID, "acl",
			map[string]any{"acl_version": item.aclVersion}); err != nil {
			return err
		}
	}
	return nil
}

// handleNoteMembershipRestoreTx runs inside the transaction that re-adds a user
// to a Space.
//
// Space notes never leave with their creator, so membership restoration needs
// no note-specific work.
func handleNoteMembershipRestoreTx(_ context.Context, _ *sql.Tx, _, _ string) error {
	return nil
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
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		// Only notes whose purge command actually reached the collaboration
		// service are removed. Deleting the row first would strand the Durable
		// Object with no record of what to clean up.
		result, err := tx.ExecContext(ctx,
			`DELETE FROM space_notes WHERE id IN (
			     SELECT n.id FROM space_notes n
			     WHERE ((n.lifecycle_state=$1 AND n.purge_after<=NOW())
			        OR (n.lifecycle_state=$2 AND EXISTS(
			            SELECT 1 FROM space_note_control_outbox o
			            WHERE o.note_id=n.id AND o.command='purge' AND o.delivered_at IS NOT NULL)))
			       AND NOT EXISTS (
			           SELECT 1 FROM space_note_assets a
			           WHERE a.note_id=n.id AND a.lifecycle_state<>'deleted')
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

// noteControlMaxAttempts is how often a note command is tried before it is
// given up on: about twenty minutes of retries, the last ones a minute apart.
const noteControlMaxAttempts = 25

// noteControlOpen is the outbox rows still to deliver, aliased o.
const noteControlOpen = `o.delivered_at IS NULL AND o.abandoned_at IS NULL`

// noteControlHead keeps each note's commands in the order they were queued:
// only a note's oldest open command may go, so a replace never overtakes the
// bootstrap before it, nor an ACL change the one it follows.
const noteControlHead = noteControlOpen + ` AND NOT EXISTS (
	SELECT 1 FROM space_note_control_outbox p
	WHERE p.note_id=o.note_id AND p.delivered_at IS NULL AND p.abandoned_at IS NULL
	  AND (p.created_at,p.id) < (o.created_at,o.id))`

// PendingNoteControlCommands claims a bounded batch of undelivered commands,
// at most one per note, and pushes their next attempt out, so two workers
// cannot pick up the same command.
func (db *Database) PendingNoteControlCommands(ctx context.Context, limit int) ([]NoteControlCommand, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	commands := []NoteControlCommand{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`UPDATE space_note_control_outbox SET attempts=attempts+1,
			        next_attempt_at=NOW()+(LEAST(attempts+1,6)*INTERVAL '10 seconds')
			 WHERE id IN (
			     SELECT o.id FROM space_note_control_outbox o
			     WHERE `+noteControlHead+` AND o.next_attempt_at<=NOW()
			     ORDER BY o.created_at,o.id FOR UPDATE SKIP LOCKED LIMIT $1)
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
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_note_control_outbox SET delivered_at=NOW(),last_error=''
			 WHERE id=$1 AND delivered_at IS NULL`, commandID)
		return err
	})
}

// MarkNoteControlFailed records why a delivery failed so the backlog is
// diagnosable. The row stays pending and is retried, until it has been tried
// noteControlMaxAttempts times; then it is given up on so the note's later
// commands can go.
func (db *Database) MarkNoteControlFailed(ctx context.Context, commandID, reason string) error {
	return db.markNoteControlFailed(ctx, commandID, reason, false)
}

// AbandonNoteControlCommand gives up on a command the collaboration service
// refused as malformed: sending it again would be refused again.
func (db *Database) AbandonNoteControlCommand(ctx context.Context, commandID, reason string) error {
	return db.markNoteControlFailed(ctx, commandID, reason, true)
}

func (db *Database) markNoteControlFailed(ctx context.Context, commandID, reason string, abandon bool) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE space_note_control_outbox SET last_error=$1,
			        abandoned_at=CASE WHEN $3 OR attempts>=$4 THEN NOW() END
			 WHERE id=$2 AND delivered_at IS NULL AND abandoned_at IS NULL`,
			reason, commandID, abandon, noteControlMaxAttempts)
		return err
	})
}

// NoteControlBacklog reports undelivered commands that are already due, for
// readiness metrics. A small transient backlog is normal.
func (db *Database) NoteControlBacklog(ctx context.Context) (int64, error) {
	var backlog int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM space_note_control_outbox o WHERE `+noteControlHead+` AND o.next_attempt_at<=NOW()`).
			Scan(&backlog)
	})
	return backlog, err
}
