package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Note lifecycle states.
const (
	NoteLifecycleActive              = "active"
	NoteLifecycleArchived            = "archived"
	NoteLifecycleArchivedCreatorLeft = "archived_creator_left"
	NoteLifecycleDeleting            = "deleting"
)

// Note roles. The creator's role is implicit and never stored.
const (
	NoteRoleCreator = "creator"
	NoteRoleEditor  = "editor"
	NoteRoleViewer  = "viewer"
)

// NoteAccess is the single answer to "what may this user do with this note".
// Every note handler must obtain it from NoteAccessFor rather than deriving
// capabilities itself, so the creator-only-administrator rule cannot drift
// between call sites.
type NoteAccess struct {
	CanView   bool
	CanEdit   bool
	CanDelete bool
	Role      string
}

// noteAccessDenied is the response for every unauthorized case. It is
// deliberately identical whether the note is missing, archived, or simply not
// shared with the caller, so an unauthorized caller cannot distinguish them.
var noteAccessDenied = NoteAccess{}

// NoteAccessFor resolves a caller's capabilities for one note.
//
// Every current Space member may view and edit a native note. The creator and
// Space owner may archive/delete it. There is no per-note ACL in beta.
func (db *Database) NoteAccessFor(ctx context.Context, userID, noteID string) (NoteAccess, error) {
	var access NoteAccess
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
	var creatorUserID, spaceID, lifecycle, audienceKind, audienceConversationID string
	err := tx.QueryRowContext(ctx,
		`SELECT creator_user_id,space_id,lifecycle_state,audience_kind,COALESCE(audience_conversation_id,'') FROM space_notes WHERE id=$1`,
		noteID).Scan(&creatorUserID, &spaceID, &lifecycle, &audienceKind, &audienceConversationID)
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
	if audienceKind == SpaceAudienceConversation {
		var participant bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members WHERE conversation_id=$1 AND actor_kind='person' AND user_id=$2)`, audienceConversationID, userID).Scan(&participant); err != nil {
			return noteAccessDenied, err
		}
		if !participant {
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

// noteEventVisibleToUserTx decides whether one note.* realtime event may be
// delivered to or replayed for a user.
//
// This deliberately differs from NoteAccessFor in one way: it does not consult
// lifecycle state. A note.archived event exists precisely to tell the creator
// and grantees that the note went away, and checking "can view" would suppress
// exactly the event they need in order to drop it from their list.
//
// It fails closed. Once the row is hard-deleted a note.deleted event still
// inside the replay window resolves to nobody; those clients refetch their
// list on reconnect and simply will not see the note.
func noteEventVisibleToUserTx(ctx context.Context, tx *sql.Tx, userID string, event SpaceEvent) (bool, error) {
	if event.EntityID == "" {
		return false, nil
	}
	var creatorUserID, spaceID string
	err := tx.QueryRowContext(ctx,
		`SELECT creator_user_id,space_id FROM space_notes WHERE id=$1`,
		event.EntityID).Scan(&creatorUserID, &spaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// The event's own Space must match the note's, so a stale or forged
	// entity ID cannot pull a note's events into another Space's stream.
	if spaceID != event.SpaceID {
		return false, nil
	}
	// A former member loses visibility even while a stale grant row survives.
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		if errors.Is(err, ErrSpaceForbidden) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// recordNoteEventTx records a note.* Space event.
//
// The payload carries IDs and safe metadata only. It never contains note
// content, Yjs updates, or the grant set: the same bytes reach every
// authorized recipient, so anything in here is visible to all of them.
func recordNoteEventTx(ctx context.Context, tx *sql.Tx, spaceID, actorUserID, eventType, noteID string, extra map[string]any) error {
	payload := map[string]any{"note_id": noteID}
	for key, value := range extra {
		payload[key] = value
	}
	_, err := recordSpaceEventTx(ctx, tx, spaceID, actorUserID, eventType, noteID, payload)
	return err
}

// SpaceNote is the server-owned metadata for one collaborative note. The
// document body lives only in the collaboration service; the projections here
// exist for listing and search.
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
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	// Role is the caller's own effective role. A non-creator never receives the
	// full grant set, only this.
	Role          string `json:"role"`
	CanDelete     bool   `json:"can_delete"`
	BacklinkCount int64  `json:"backlink_count"`
}

// CreateSpaceNote creates a note shared with every current Space member.
func (db *Database) CreateSpaceNote(ctx context.Context, creatorUserID, spaceID, title string) (*SpaceNote, error) {
	return db.CreateSpaceNoteWithAudience(ctx, creatorUserID, spaceID, title, SpaceResourceAudience{Kind: SpaceAudienceSpace}, "")
}

func (db *Database) CreateSpaceNoteWithAudience(ctx context.Context, creatorUserID, spaceID, title string, audience SpaceResourceAudience, markdown string) (*SpaceNote, error) {
	if creatorUserID == "" || spaceID == "" {
		return nil, ErrSpaceInvalid
	}
	note := &SpaceNote{
		ID: "note_" + uuid.NewString(), SpaceID: spaceID, CreatorUserID: creatorUserID,
		TitleProjection: title, LifecycleState: NoteLifecycleActive, ACLVersion: 1,
		Role: NoteRoleCreator, CanDelete: true,
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, creatorUserID); err != nil {
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
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO space_notes(id,space_id,creator_user_id,title_projection,audience_kind,audience_conversation_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''))
			 RETURNING created_at,updated_at`,
			note.ID, spaceID, creatorUserID, title, normalized.Kind, normalized.ConversationID).Scan(&note.CreatedAt, &note.UpdatedAt); err != nil {
			return err
		}
		if strings.TrimSpace(markdown) != "" {
			if err := enqueueNoteControlTx(ctx, tx, note.ID, "bootstrap", map[string]any{"title": title, "markdown": markdown}); err != nil {
				return err
			}
		}
		return recordNoteEventTx(ctx, tx, note.SpaceID, creatorUserID, "note.created", note.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return note, nil
}

// AccessibleSpaceNotes lists the active notes a caller may view in one Space.
func (db *Database) AccessibleSpaceNotes(ctx context.Context, userID, spaceID string) ([]SpaceNote, error) {
	notes := []SpaceNote{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT n.id,n.space_id,n.creator_user_id,n.title_projection,n.markdown_projection,n.plain_text_projection,n.lifecycle_state,
			        n.collaboration_revision,n.acl_version,n.audience_kind,COALESCE(n.audience_conversation_id,''),n.created_at,n.updated_at,
			        CASE WHEN n.creator_user_id=$1 THEN 'creator' ELSE 'editor' END AS effective_role,
			        (n.creator_user_id=$1 OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=n.space_id AND s.owner_user_id=$1)) AS can_delete,
			        (SELECT COUNT(*) FROM space_note_links links
			         JOIN space_notes source ON source.id=links.source_note_id
			         WHERE links.target_note_id=n.id AND source.lifecycle_state='active'
			           AND (source.audience_kind='space' OR EXISTS(
			               SELECT 1 FROM space_conversation_members cm
			               WHERE cm.conversation_id=source.audience_conversation_id
			                 AND cm.actor_kind='person' AND cm.user_id=$1))) AS backlink_count
			 FROM space_notes n
			 WHERE n.space_id=$2 AND n.lifecycle_state='active'
			   AND (n.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=n.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$1))
			 ORDER BY n.updated_at DESC`, userID, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var note SpaceNote
			if err := rows.Scan(&note.ID, &note.SpaceID, &note.CreatorUserID, &note.TitleProjection,
				&note.MarkdownProjection, &note.PlainTextProjection, &note.LifecycleState, &note.CollaborationRevision, &note.ACLVersion, &note.AudienceKind, &note.AudienceConversationID,
				&note.CreatedAt, &note.UpdatedAt, &note.Role, &note.CanDelete, &note.BacklinkCount); err != nil {
				return err
			}
			notes = append(notes, note)
		}
		return rows.Err()
	})
	return notes, err
}
