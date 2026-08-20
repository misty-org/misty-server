package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// UpdateSpaceNoteContent is the server-owned Agent write boundary for native
// Notes. The projection and collaboration command are committed together; the
// retryable outbox makes delivery idempotent across runtime restarts.
func (db *Database) UpdateSpaceNoteContent(ctx context.Context, userID, noteID, title, markdown string) (*SpaceNote, error) {
	title = strings.TrimSpace(title)
	markdown = strings.TrimSpace(markdown)
	if title == "" || len([]rune(title)) > 500 || len([]rune(markdown)) > 100_000 {
		return nil, ErrSpaceInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrSpaceNotFound
		}
		var spaceID string
		if err := tx.QueryRowContext(ctx,
			`SELECT space_id FROM space_notes WHERE id=$1 AND lifecycle_state='active'`, noteID).
			Scan(&spaceID); err != nil {
			return err
		}
		if err := enqueueNoteControlTx(ctx, tx, noteID, "replace_markdown", map[string]any{"title": title, "markdown": markdown}); err != nil {
			return err
		}
		return recordNoteEventTx(ctx, tx, spaceID, userID, "note.replacement.pending", noteID, nil)
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceNoteByID(ctx, userID, noteID)
}

// SpaceNoteByID returns one note the caller may view, with the caller's own
// effective role attached. Any caller who cannot view it gets ErrSpaceNotFound.
func (db *Database) SpaceNoteByID(ctx context.Context, userID, noteID string) (*SpaceNote, error) {
	note := &SpaceNote{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrSpaceNotFound
		}
		note.Role = access.Role
		note.CanDelete = access.CanDelete
		return tx.QueryRowContext(ctx,
			`SELECT id,space_id,creator_user_id,title_projection,markdown_projection,plain_text_projection,
			        lifecycle_state,collaboration_revision,acl_version,audience_kind,COALESCE(audience_conversation_id,''),created_at,updated_at,
			        (SELECT COUNT(*) FROM space_note_links links
			         JOIN space_notes source ON source.id=links.source_note_id
			         WHERE links.target_note_id=space_notes.id AND source.lifecycle_state='active'
			           AND (source.audience_kind='space' OR EXISTS(
			               SELECT 1 FROM space_conversation_members cm
			               WHERE cm.conversation_id=source.audience_conversation_id
			                 AND cm.actor_kind='person' AND cm.user_id=$2)))
			 FROM space_notes WHERE id=$1`, noteID, userID).Scan(
			&note.ID, &note.SpaceID, &note.CreatorUserID, &note.TitleProjection,
			&note.MarkdownProjection, &note.PlainTextProjection, &note.LifecycleState, &note.CollaborationRevision,
			&note.ACLVersion, &note.AudienceKind, &note.AudienceConversationID, &note.CreatedAt, &note.UpdatedAt, &note.BacklinkCount)
	})
	if err != nil {
		return nil, err
	}
	return note, nil
}

// UpdateNoteSharedTags replaces the server-owned tag list.
//
// Tags are deliberately not part of the CRDT: they are Space metadata rather
// than document content, so they are edited here rather than through the
// collaborative document. Any editor may change them.
func (db *Database) UpdateNoteSharedTags(ctx context.Context, userID, noteID string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	if len(tags) > 50 {
		return ErrSpaceInvalid
	}
	for _, tag := range tags {
		if tag == "" || len(tag) > 80 {
			return ErrSpaceInvalid
		}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			// A viewer must not be able to tell an editable note from a
			// nonexistent one by the error it gets back.
			return ErrSpaceNotFound
		}
		var spaceID string
		if err := tx.QueryRowContext(ctx,
			`UPDATE space_notes SET shared_tags=$1,updated_at=NOW() WHERE id=$2 RETURNING space_id`,
			raw, noteID).Scan(&spaceID); err != nil {
			return err
		}
		return recordNoteEventTx(ctx, tx, spaceID, userID, "note.projection.updated", noteID, nil)
	})
}

// SetSpaceNoteArchived archives or restores a note. Only its creator or the
// Space owner may change this lifecycle state.
func (db *Database) SetSpaceNoteArchived(ctx context.Context, userID, noteID string, archived bool) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var creatorUserID, spaceID, lifecycle string
		if err := tx.QueryRowContext(ctx,
			`SELECT creator_user_id,space_id,lifecycle_state FROM space_notes WHERE id=$1 FOR UPDATE`,
			noteID).Scan(&creatorUserID, &spaceID, &lifecycle); err != nil {
			return ErrSpaceNotFound
		}
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil || (userID != creatorUserID && role != "owner") {
			return ErrSpaceNotFound
		}
		target := NoteLifecycleActive
		eventType := "note.restored"
		if archived {
			target = NoteLifecycleArchived
			eventType = "note.archived"
		}
		if lifecycle == target {
			return nil
		}
		if lifecycle != NoteLifecycleActive && lifecycle != NoteLifecycleArchived {
			return ErrSpaceNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_notes
			SET lifecycle_state=$1,archived_at=CASE WHEN $1='archived' THEN NOW() ELSE NULL END,
			    purge_after=NULL,updated_at=NOW()
			WHERE id=$2`, target, noteID); err != nil {
			return err
		}
		return recordNoteEventTx(ctx, tx, spaceID, userID, eventType, noteID, nil)
	})
}

// DeleteSpaceNote begins hard deletion. It is creator/owner-only and idempotent: a
// retried request against an already-deleting note succeeds without a second
// event, so a client that times out and retries does not see a conflict.
//
// The row moves to 'deleting' rather than being removed here. The collaboration
// room and R2 assets must be purged first, and keeping the row until then is
// also what lets the note.deleted event resolve its audience.
func (db *Database) DeleteSpaceNote(ctx context.Context, userID, noteID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanDelete {
			// Covers ordinary members and a genuinely missing
			// note with one indistinguishable answer.
			if lifecycle, lookupErr := noteLifecycleTx(ctx, tx, noteID); lookupErr != nil {
				return lookupErr
			} else if lifecycle == NoteLifecycleDeleting {
				// Already deleting: the creator's retry lands here because
				// NoteAccessFor denies non-active notes.
				if allowed, actorErr := noteDestructiveActorTx(ctx, tx, noteID, userID); actorErr != nil {
					return actorErr
				} else if allowed {
					return nil
				}
			}
			return ErrSpaceNotFound
		}
		var spaceID string
		if err := tx.QueryRowContext(ctx,
			`UPDATE space_notes SET lifecycle_state=$1,acl_version=acl_version+1,updated_at=NOW()
			 WHERE id=$2 AND lifecycle_state IN ($3,$4) RETURNING space_id`,
			NoteLifecycleDeleting, noteID, NoteLifecycleActive, NoteLifecycleArchived).Scan(&spaceID); err != nil {
			return err
		}
		if err := recordNoteEventTx(ctx, tx, spaceID, userID, "note.deleted", noteID, nil); err != nil {
			return err
		}
		// Files must remain referenced until the Journal asset worker has either
		// deleted their blob or proved that another live reference owns it.
		if _, err := tx.ExecContext(ctx, `UPDATE space_note_assets
			SET lifecycle_state='deleting',deleted_at=COALESCE(deleted_at,NOW())
			WHERE note_id=$1 AND lifecycle_state IN ('ready','unreferenced')`, noteID); err != nil {
			return err
		}
		// The room must be torn down even if the control call fails right now,
		// so the command is queued rather than issued inline.
		return enqueueNoteControlTx(ctx, tx, noteID, "purge", nil)
	})
}

func noteLifecycleTx(ctx context.Context, tx *sql.Tx, noteID string) (string, error) {
	var lifecycle string
	err := tx.QueryRowContext(ctx, `SELECT lifecycle_state FROM space_notes WHERE id=$1`, noteID).Scan(&lifecycle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return lifecycle, err
}

func noteCreatorTx(ctx context.Context, tx *sql.Tx, noteID string) (string, error) {
	var creator string
	err := tx.QueryRowContext(ctx, `SELECT creator_user_id FROM space_notes WHERE id=$1`, noteID).Scan(&creator)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return creator, err
}

func noteDestructiveActorTx(
	ctx context.Context,
	tx *sql.Tx,
	noteID, userID string,
) (bool, error) {
	var creatorUserID, spaceID string
	if err := tx.QueryRowContext(ctx,
		`SELECT creator_user_id,space_id FROM space_notes WHERE id=$1`,
		noteID).Scan(&creatorUserID, &spaceID); err != nil {
		return false, err
	}
	role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
	if err != nil {
		return false, err
	}
	return creatorUserID == userID || role == "owner", nil
}

// enqueueNoteControlTx queues a command for the collaboration service. Delivery
// is the outbox worker's job, so an unreachable service can never block or roll
// back the authorization transaction that produced the command.
func enqueueNoteControlTx(ctx context.Context, tx *sql.Tx, noteID, command string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO space_note_control_outbox(id,note_id,command,payload) VALUES($1,$2,$3,$4)`,
		"notectl_"+uuid.NewString(), noteID, command, raw)
	return err
}

// RequireNoteView returns ErrSpaceNotFound for any caller who cannot view the
// note. Callers must surface it as a plain not-found: the title, creator,
// timestamps, asset names, and even the note's existence stay hidden.
func (db *Database) RequireNoteView(ctx context.Context, userID, noteID string) (NoteAccess, error) {
	access, err := db.NoteAccessFor(ctx, userID, noteID)
	if err != nil {
		return noteAccessDenied, err
	}
	if !access.CanView {
		return noteAccessDenied, ErrSpaceNotFound
	}
	return access, nil
}
