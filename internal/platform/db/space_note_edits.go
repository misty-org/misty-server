package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (db *Database) UpdateSpaceNoteContent(ctx context.Context, userID, noteID, title, markdown string) (*SpaceNote, error) {
	title = strings.TrimSpace(title)
	markdown = strings.TrimSpace(markdown)
	if title == "" || len([]rune(title)) > 500 || len([]rune(markdown)) > 100_000 {
		return nil, ErrSpaceInvalid
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrSpaceNotFound
		}
		var spaceID, creatorUserID string
		if err := tx.QueryRowContext(ctx,
			`SELECT space_id,creator_user_id FROM space_notes WHERE id=$1 AND lifecycle_state='active' FOR UPDATE`, noteID).
			Scan(&spaceID, &creatorUserID); err != nil {
			return err
		}
		if err := writeNoteContentTx(ctx, tx, noteID, creatorUserID, spaceID, title, markdown); err != nil {
			return err
		}
		return recordNoteEventTx(ctx, tx, spaceID, userID, "note.projection.updated", noteID, nil)
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceNoteByID(ctx, userID, noteID)
}

func (db *Database) SpaceNoteByID(ctx context.Context, userID, noteID string) (*SpaceNote, error) {
	note := &SpaceNote{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
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
			        lifecycle_state,collaboration_revision,acl_version,audience_kind,COALESCE(audience_conversation_id,''),visibility,created_at,updated_at,
			        (SELECT COUNT(*) FROM space_note_links links
			         JOIN space_notes source ON source.id=links.source_note_id
			         WHERE links.target_note_id=space_notes.id AND source.lifecycle_state='active'
			           AND (source.visibility='public' OR source.creator_user_id=$3)
			           AND (source.audience_kind='space' OR public.misty_can_read_space_conversation(source.audience_conversation_id,$2)))
			 FROM space_notes WHERE id=$1`, noteID, userID, userID).Scan(
			&note.ID, &note.SpaceID, &note.CreatorUserID, &note.TitleProjection,
			&note.MarkdownProjection, &note.PlainTextProjection, &note.LifecycleState, &note.CollaborationRevision,
			&note.ACLVersion, &note.AudienceKind, &note.AudienceConversationID, &note.Visibility, &note.CreatedAt, &note.UpdatedAt, &note.BacklinkCount)
	})
	if err != nil {
		return nil, err
	}
	return note, nil
}

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
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
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

func (db *Database) SetSpaceNoteArchived(ctx context.Context, userID, noteID string, archived bool) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
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

func (db *Database) DeleteSpaceNote(ctx context.Context, userID, noteID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanDelete {
			if lifecycle, lookupErr := noteLifecycleTx(ctx, tx, noteID); lookupErr != nil {
				return lookupErr
			} else if lifecycle == NoteLifecycleDeleting {
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
		if _, err := tx.ExecContext(ctx, `UPDATE space_note_assets
			SET lifecycle_state='deleting',deleted_at=COALESCE(deleted_at,NOW())
			WHERE note_id=$1 AND lifecycle_state IN ('ready','unreferenced')`, noteID); err != nil {
			return err
		}
		return nil
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
