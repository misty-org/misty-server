package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	NoteLifecycleActive              = "active"
	NoteLifecycleArchived            = "archived"
	NoteLifecycleArchivedCreatorLeft = "archived_creator_left"
	NoteLifecycleDeleting            = "deleting"
)

const (
	NoteRoleCreator = "creator"
	NoteRoleEditor  = "editor"
)

type NoteAccess struct {
	CanView   bool
	CanEdit   bool
	CanDelete bool
	Role      string
}

var noteAccessDenied = NoteAccess{}

func (db *Database) NoteAccessFor(ctx context.Context, userID, noteID string) (NoteAccess, error) {
	var access NoteAccess
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var innerErr error
		access, innerErr = noteAccessForTx(ctx, tx, userID, noteID)
		return innerErr
	})
	return access, err
}

func noteAccessForTx(ctx context.Context, tx *sql.Tx, userID, noteID string) (NoteAccess, error) {
	if userID == "" || noteID == "" {
		return noteAccessDenied, nil
	}
	var creatorUserID, spaceID, lifecycle, audienceKind, audienceConversationID, visibility string
	err := tx.QueryRowContext(ctx,
		`SELECT creator_user_id,space_id,lifecycle_state,audience_kind,COALESCE(audience_conversation_id,''),visibility FROM space_notes WHERE id=$1`,
		noteID).Scan(&creatorUserID, &spaceID, &lifecycle, &audienceKind, &audienceConversationID, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return noteAccessDenied, nil
	}
	if err != nil {
		return noteAccessDenied, err
	}
	role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
	if err != nil {
		if errors.Is(err, ErrSpaceForbidden) {
			return noteAccessDenied, nil
		}
		return noteAccessDenied, err
	}
	isCreator := userID == creatorUserID
	isOwner := role == "owner"
	if visibility == VisibilityPrivate && userID != creatorUserID {
		return noteAccessDenied, nil
	}
	if audienceKind == SpaceAudienceConversation {
		readable, err := conversationReadableTx(ctx, tx, userID, spaceID, audienceConversationID)
		if err != nil {
			return noteAccessDenied, err
		}
		if !readable {
			return noteAccessDenied, nil
		}
	}
	if lifecycle != NoteLifecycleActive {
		if lifecycle == NoteLifecycleArchived && (isCreator || isOwner) {
			return NoteAccess{CanDelete: true, Role: NoteRoleCreator}, nil
		}
		return noteAccessDenied, nil
	}
	if isCreator {
		return NoteAccess{CanView: true, CanEdit: true, CanDelete: true, Role: NoteRoleCreator}, nil
	}
	access := NoteAccess{CanView: true, CanEdit: true, Role: NoteRoleEditor}
	if isOwner {
		access.CanDelete = true
	}
	return access, nil
}

func noteEventVisibleToUserTx(ctx context.Context, tx *sql.Tx, userID string, event SpaceEvent) (bool, error) {
	if event.EntityID == "" {
		return false, nil
	}
	var creatorUserID, spaceID, visibility string
	err := tx.QueryRowContext(ctx,
		`SELECT creator_user_id,space_id,visibility FROM space_notes WHERE id=$1`,
		event.EntityID).Scan(&creatorUserID, &spaceID, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if spaceID != event.SpaceID {
		return false, nil
	}
	if visibility == VisibilityPrivate && userID != creatorUserID {
		return false, nil
	}
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		if errors.Is(err, ErrSpaceForbidden) {
			return false, nil
		}
		return false, err
	}
	return itemAudienceReadableTx(ctx, tx, userID, spaceID, "note", event.EntityID)
}

func recordNoteEventTx(ctx context.Context, tx *sql.Tx, spaceID, actorUserID, eventType, noteID string, extra map[string]any) error {
	payload := map[string]any{"note_id": noteID}
	for key, value := range extra {
		payload[key] = value
	}
	_, err := recordSpaceEventTx(ctx, tx, spaceID, actorUserID, eventType, noteID, payload)
	return err
}

type SpaceNote struct {
	ID                     string    `json:"id"`
	SpaceID                string    `json:"space_id"`
	CreatorUserID          string    `json:"creator_user_id"`
	TitleProjection        string    `json:"title"`
	MarkdownProjection     string    `json:"markdown,omitempty"`
	PlainTextProjection    string    `json:"plain_text,omitempty"`
	LifecycleState         string    `json:"lifecycle_state"`
	CollaborationRevision  int64     `json:"collaboration_revision"`
	ACLVersion             int64     `json:"acl_version"`
	AudienceKind           string    `json:"audience_kind"`
	AudienceConversationID string    `json:"audience_conversation_id,omitempty"`
	Visibility             string    `json:"visibility"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	Role                   string    `json:"role"`
	CanDelete              bool      `json:"can_delete"`
	BacklinkCount          int64     `json:"backlink_count"`
}

func (db *Database) CreateSpaceNote(ctx context.Context, creatorUserID, spaceID, title string) (*SpaceNote, error) {
	return db.CreateSpaceNoteWithAudience(ctx, creatorUserID, spaceID, title, SpaceResourceAudience{Kind: SpaceAudienceSpace}, "")
}

func (db *Database) CreateSpaceNoteWithAudience(ctx context.Context, creatorUserID, spaceID, title string, audience SpaceResourceAudience, markdown string) (*SpaceNote, error) {
	return db.CreateSpaceNoteWithVisibility(ctx, creatorUserID, spaceID, title, audience, markdown, "")
}

func (db *Database) CreateSpaceNoteWithVisibility(ctx context.Context, creatorUserID, spaceID, title string, audience SpaceResourceAudience, markdown, visibility string) (*SpaceNote, error) {
	if creatorUserID == "" || spaceID == "" {
		return nil, ErrSpaceInvalid
	}
	if visibility != "" && visibility != "public" && visibility != "private" {
		return nil, ErrSpaceInvalid
	}
	if body := strings.TrimSpace(markdown); body != "" {
		named := strings.TrimSpace(title)
		if named == "" || len([]rune(named)) > 500 || len([]rune(body)) > 100_000 {
			return nil, ErrSpaceInvalid
		}
	}
	note := &SpaceNote{
		ID: "note_" + uuid.NewString(), SpaceID: spaceID, CreatorUserID: creatorUserID,
		TitleProjection: title, LifecycleState: NoteLifecycleActive, ACLVersion: 1,
		Role: NoteRoleCreator, CanDelete: true,
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, creatorUserID); err != nil {
			return err
		}
		if err := requireStorageNotFullTx(ctx, tx, creatorUserID, spaceID); err != nil {
			return err
		}
		normalized, err := NormalizeResourceAudience(audience.Kind, audience.ConversationID)
		if err != nil {
			return err
		}
		if err := validateResourceAudienceTx(ctx, tx, creatorUserID, spaceID, normalized); err != nil {
			return err
		}
		note.AudienceKind, note.AudienceConversationID = normalized.Kind, normalized.ConversationID
		insert := `INSERT INTO space_notes(id,space_id,creator_user_id,title_projection,audience_kind,audience_conversation_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''))
			 RETURNING created_at,updated_at,visibility`
		args := []any{note.ID, spaceID, creatorUserID, title, normalized.Kind, normalized.ConversationID}
		if visibility != "" {
			insert = `INSERT INTO space_notes(id,space_id,creator_user_id,title_projection,audience_kind,audience_conversation_id,visibility) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7)
			 RETURNING created_at,updated_at,visibility`
			args = append(args, visibility)
		}
		if err := tx.QueryRowContext(ctx, insert, args...).Scan(&note.CreatedAt, &note.UpdatedAt, &note.Visibility); err != nil {
			return err
		}
		if body := strings.TrimSpace(markdown); body != "" {
			if err := writeNoteContentTx(ctx, tx, note.ID, creatorUserID, spaceID, strings.TrimSpace(title), body); err != nil {
				return err
			}
			note.TitleProjection = strings.TrimSpace(title)
			note.MarkdownProjection = body
			note.PlainTextProjection = noteMarkdownPlainText(body)
			note.CollaborationRevision = 1
		}
		return recordNoteEventTx(ctx, tx, note.SpaceID, creatorUserID, "note.created", note.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return note, nil
}

func (db *Database) AccessibleSpaceNotes(ctx context.Context, userID, spaceID string) ([]SpaceNote, error) {
	notes := []SpaceNote{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT n.id,n.space_id,n.creator_user_id,n.title_projection,n.markdown_projection,n.plain_text_projection,n.lifecycle_state,
			        n.collaboration_revision,n.acl_version,n.audience_kind,COALESCE(n.audience_conversation_id,''),n.visibility,n.created_at,n.updated_at,
			        CASE WHEN n.creator_user_id=$1 THEN 'creator' ELSE 'editor' END AS effective_role,
			        (n.creator_user_id=$1 OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=n.space_id AND s.owner_user_id=$1)) AS can_delete,
			        (SELECT COUNT(*) FROM space_note_links links
			         JOIN space_notes source ON source.id=links.source_note_id
			         WHERE links.target_note_id=n.id AND source.lifecycle_state='active'
			           AND (source.visibility='public' OR source.creator_user_id=$3)
			           AND (source.audience_kind='space' OR public.misty_can_read_space_conversation(source.audience_conversation_id,$1))) AS backlink_count
			 FROM space_notes n
			 WHERE n.space_id=$2 AND n.lifecycle_state='active'
			   AND (n.visibility='public' OR n.creator_user_id=$3)
			   AND (n.audience_kind='space' OR public.misty_can_read_space_conversation(n.audience_conversation_id,$1))
			 ORDER BY n.updated_at DESC`, userID, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var note SpaceNote
			if err := rows.Scan(&note.ID, &note.SpaceID, &note.CreatorUserID, &note.TitleProjection,
				&note.MarkdownProjection, &note.PlainTextProjection, &note.LifecycleState, &note.CollaborationRevision, &note.ACLVersion, &note.AudienceKind, &note.AudienceConversationID,
				&note.Visibility, &note.CreatedAt, &note.UpdatedAt, &note.Role, &note.CanDelete, &note.BacklinkCount); err != nil {
				return err
			}
			notes = append(notes, note)
		}
		return rows.Err()
	})
	return notes, err
}

func requireStorageNotFullTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	if err := prepareStorageReservationTx(ctx, tx, userID, spaceID); err != nil {
		return err
	}
	return RequireStorageRoomTx(ctx, tx, userID, spaceID, 1)
}
