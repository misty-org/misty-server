package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Decks and workbooks: the made items built on one shared implementation, modelled exactly on
// drawings. The Yjs document lives in the item's collaboration room; this row is the server's
// record of it (title, audience, layer, ACL version) and its projection (plain text for search,
// the room's size, the item's slide or cell count). Every current member who can see an item
// may edit it; its creator and the Space's owner may delete it.

// SpaceMadeItem is a deck or a workbook. Its JSON has a drawing's fields (SpaceDrawing), plus
// its projection.
type SpaceMadeItem struct {
	ID                     string    `json:"id"`
	SpaceID                string    `json:"space_id"`
	CreatorUserID          string    `json:"creator_user_id"`
	Title                  string    `json:"title"`
	LifecycleState         string    `json:"lifecycle_state"`
	CollaborationRevision  int64     `json:"collaboration_revision"`
	ACLVersion             int64     `json:"acl_version"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	Role                   string    `json:"role"`
	CanDelete              bool      `json:"can_delete"`
	AudienceKind           string    `json:"audience_kind"`
	AudienceConversationID string    `json:"audience_conversation_id,omitempty"`
	// Visibility is "public" or "private": a private item opens only for its creator.
	Visibility string `json:"visibility"`
	// PlainText is the search projection, at most 100k characters. Only a single item's read
	// carries it; lists leave it out.
	PlainText string `json:"plain_text,omitempty"`
	// ContentBytes is the room's snapshot size and HistoryBytes its kept versions; both count
	// toward storage.
	ContentBytes int64 `json:"content_bytes"`
	HistoryBytes int64 `json:"history_bytes"`
	// SlideCount (decks) or CellCount (workbooks): the size the level's limit counts.
	SlideCount *int64 `json:"slide_count,omitempty"`
	CellCount  *int64 `json:"cell_count,omitempty"`
	// OverLimit: the item is at or past its Space level's deck_slides or workbook_cells limit,
	// so its room refuses anything that would make it bigger. It stays readable and editable.
	OverLimit bool `json:"over_limit,omitempty"`
}

// SpaceDeck and SpaceWorkbook name the shared shape for each kind.
type (
	SpaceDeck     = SpaceMadeItem
	SpaceWorkbook = SpaceMadeItem
)

// MadeItemAccess is the capability set one caller has on one made item.
type MadeItemAccess = DrawingAccess

// madeItemKind returns a shared kind by name or section.
func madeItemKind(name string) (SpaceItemKind, error) {
	kind, ok := SpaceItemKindNamed(name)
	if !ok || !kind.Shared {
		return SpaceItemKind{}, ErrSpaceInvalid
	}
	return kind, nil
}

// madeItemColumns is every column a made item's record carries, aliased as m. The table's own
// count column comes last.
func madeItemColumns(kind SpaceItemKind) string {
	return `m.id,m.space_id,m.creator_user_id,m.title,m.lifecycle_state,
	        m.collaboration_revision,m.acl_version,m.created_at,m.updated_at,
	        m.audience_kind,COALESCE(m.audience_conversation_id,''),m.visibility,
	        m.content_bytes,m.history_bytes,m.` + kind.CountColumn
}

func scanMadeItem(kind SpaceItemKind, scanner interface{ Scan(...any) error }, item *SpaceMadeItem, extra ...any) error {
	var count int64
	targets := []any{
		&item.ID, &item.SpaceID, &item.CreatorUserID, &item.Title, &item.LifecycleState,
		&item.CollaborationRevision, &item.ACLVersion, &item.CreatedAt, &item.UpdatedAt,
		&item.AudienceKind, &item.AudienceConversationID, &item.Visibility,
		&item.ContentBytes, &item.HistoryBytes, &count,
	}
	if err := scanner.Scan(append(targets, extra...)...); err != nil {
		return err
	}
	switch kind.Name {
	case SpaceItemDeck:
		item.SlideCount = &count
	case SpaceItemWorkbook:
		item.CellCount = &count
	}
	return nil
}

// madeItemCount is the item's size in its limit's unit.
func madeItemCount(item *SpaceMadeItem) int64 {
	switch {
	case item.SlideCount != nil:
		return *item.SlideCount
	case item.CellCount != nil:
		return *item.CellCount
	}
	return 0
}

// NormalizeMadeItemTitle trims a title and gives an empty one the kind's default.
func NormalizeMadeItemTitle(kind SpaceItemKind, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = kind.DefaultTitle
	}
	if utf8.RuneCountInString(title) > 200 {
		return "", ErrSpaceInvalid
	}
	return title, nil
}

func recordMadeItemEventTx(ctx context.Context, tx *sql.Tx, kind SpaceItemKind, spaceID, actorUserID, event, itemID string) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, actorUserID, kind.EventFamily+event, itemID,
		map[string]any{kind.ControlColumn: itemID})
	return err
}

// requireStorageNotFullTx refuses a new item in a Space, or for an account, whose storage is
// already full. What's there stays readable and editable; only adding more is refused, the
// same way an upload is.
func requireStorageNotFullTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	if err := prepareStorageReservationTx(ctx, tx, userID, spaceID); err != nil {
		return err
	}
	return RequireStorageRoomTx(ctx, tx, userID, spaceID, 1)
}

// CreateSpaceMadeItem creates a deck or workbook for a current member, in the whole Space.
func (db *Database) CreateSpaceMadeItem(ctx context.Context, kindName, creatorUserID, spaceID, title string) (*SpaceMadeItem, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return nil, err
	}
	title, err = NormalizeMadeItemTitle(kind, title)
	if err != nil || creatorUserID == "" || spaceID == "" {
		return nil, ErrSpaceInvalid
	}
	item := &SpaceMadeItem{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, creatorUserID); err != nil {
			return err
		}
		if err := requireStorageNotFullTx(ctx, tx, creatorUserID, spaceID); err != nil {
			return err
		}
		// Table names come from the registry.
		if err := scanMadeItem(kind, tx.QueryRowContext(ctx,
			`INSERT INTO `+kind.Table+` AS m(id,space_id,creator_user_id,title)
			 VALUES($1,$2,$3,$4) RETURNING `+madeItemColumns(kind),
			kind.IDPrefix+uuid.NewString(), spaceID, creatorUserID, title,
		), item); err != nil {
			return err
		}
		item.Role, item.CanDelete = DrawingRoleCreator, true
		return recordMadeItemEventTx(ctx, tx, kind, spaceID, creatorUserID, "created", item.ID)
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

// AccessibleSpaceMadeItems lists a Space's active items of one kind that the member may see,
// most recently changed first.
func (db *Database) AccessibleSpaceMadeItems(ctx context.Context, kindName, userID, spaceID string) ([]SpaceMadeItem, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return nil, err
	}
	items := []SpaceMadeItem{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		limit, err := madeItemCountLimitTx(ctx, tx, kind, spaceID)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT `+madeItemColumns(kind)+`,
			        CASE WHEN m.creator_user_id=$1 THEN 'creator' ELSE 'editor' END,
			        (m.creator_user_id=$1 OR s.owner_user_id=$1)
			 FROM `+kind.Table+` m JOIN spaces s ON s.id=m.space_id
			 WHERE m.space_id=$2 AND m.lifecycle_state='active'
			   AND (m.visibility='public' OR m.creator_user_id=$1)
			   AND `+resourceAudienceSQL("m", "$1")+`
			 ORDER BY m.updated_at DESC`, userID, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceMadeItem
			if err := scanMadeItem(kind, rows, &item, &item.Role, &item.CanDelete); err != nil {
				return err
			}
			item.OverLimit = limit != nil && madeItemCount(&item) >= *limit
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// MadeItemAccessFor resolves one caller's capabilities without revealing missing items.
func (db *Database) MadeItemAccessFor(ctx context.Context, kindName, userID, itemID string) (MadeItemAccess, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return MadeItemAccess{}, err
	}
	var access MadeItemAccess
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		access, err = madeItemAccessForTx(ctx, tx, kind, userID, itemID)
		return err
	})
	return access, err
}

// madeItemAccessForTx is drawingAccessForTx for any shared kind: a private item opens only for
// its creator, a conversation's item only while the caller can read the conversation, and only
// a current member reaches any of it.
func madeItemAccessForTx(ctx context.Context, tx *sql.Tx, kind SpaceItemKind, userID, itemID string) (MadeItemAccess, error) {
	var creatorID, spaceID, lifecycle, audienceKind, conversationID, visibility string
	err := tx.QueryRowContext(ctx,
		`SELECT creator_user_id,space_id,lifecycle_state,audience_kind,
		        COALESCE(audience_conversation_id,''),visibility
		 FROM `+kind.Table+` WHERE id=$1`, itemID,
	).Scan(&creatorID, &spaceID, &lifecycle, &audienceKind, &conversationID, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return MadeItemAccess{}, nil
	}
	if err != nil {
		return MadeItemAccess{}, err
	}
	if visibility == VisibilityPrivate && userID != creatorID {
		return MadeItemAccess{}, nil
	}
	if audienceKind == SpaceAudienceConversation {
		visible, err := conversationReadableTx(ctx, tx, userID, spaceID, conversationID)
		if err != nil || !visible {
			return MadeItemAccess{}, err
		}
	}
	memberRole, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
	if errors.Is(err, ErrSpaceForbidden) || lifecycle != DrawingLifecycleActive {
		return MadeItemAccess{}, nil
	}
	if err != nil {
		return MadeItemAccess{}, err
	}
	access := MadeItemAccess{
		CanView: true, CanEdit: true,
		CanDelete: creatorID == userID || memberRole == "owner",
		Role:      DrawingRoleEditor,
	}
	if creatorID == userID {
		access.Role = DrawingRoleCreator
	}
	return access, nil
}

// SpaceMadeItemByID returns one item, with its plain text, when the caller can view it.
func (db *Database) SpaceMadeItemByID(ctx context.Context, kindName, userID, itemID string) (*SpaceMadeItem, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return nil, err
	}
	item := &SpaceMadeItem{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, kind, userID, itemID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrSpaceNotFound
		}
		if err := scanMadeItem(kind, tx.QueryRowContext(ctx,
			`SELECT `+madeItemColumns(kind)+`,m.plain_text_projection FROM `+kind.Table+` m WHERE m.id=$1`, itemID,
		), item, &item.PlainText); err != nil {
			return err
		}
		item.Role, item.CanDelete = access.Role, access.CanDelete
		limit, err := madeItemCountLimitTx(ctx, tx, kind, item.SpaceID)
		item.OverLimit = limit != nil && madeItemCount(item) >= *limit
		return err
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

// RenameSpaceMadeItem changes an item's title. Every editor may. The room is told too, so the
// title in the document (deck:meta, wb:meta) follows and its next projection keeps it.
func (db *Database) RenameSpaceMadeItem(ctx context.Context, kindName, userID, itemID, title string) (*SpaceMadeItem, error) {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return nil, err
	}
	title, err = NormalizeMadeItemTitle(kind, title)
	if err != nil {
		return nil, err
	}
	item := &SpaceMadeItem{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, kind, userID, itemID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrSpaceNotFound
		}
		if err := scanMadeItem(kind, tx.QueryRowContext(ctx,
			`UPDATE `+kind.Table+` AS m SET title=$1,updated_at=NOW()
			 WHERE m.id=$2 AND m.lifecycle_state='active' RETURNING `+madeItemColumns(kind),
			title, itemID,
		), item); err != nil {
			return err
		}
		item.Role, item.CanDelete = access.Role, access.CanDelete
		if err := kind.enqueueControlTx(ctx, tx, itemID, "title", map[string]any{"title": title}); err != nil {
			return err
		}
		return recordMadeItemEventTx(ctx, tx, kind, item.SpaceID, userID, "updated", itemID)
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

// DeleteSpaceMadeItem moves an item to deleting and queues its room's purge. The row stays
// until the purge is confirmed delivered.
func (db *Database) DeleteSpaceMadeItem(ctx context.Context, kindName, userID, itemID string) error {
	kind, err := madeItemKind(kindName)
	if err != nil {
		return err
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, kind, userID, itemID)
		if err != nil {
			return err
		}
		if !access.CanDelete {
			return ErrSpaceNotFound
		}
		var spaceID string
		err = tx.QueryRowContext(ctx,
			`UPDATE `+kind.Table+` SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=NOW()
			 WHERE id=$1 AND lifecycle_state='active' RETURNING space_id`, itemID,
		).Scan(&spaceID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		return deleteMadeItemTx(ctx, tx, kind, spaceID, userID, itemID)
	})
}

// deleteMadeItemTx finishes a delete already marked on the row: the event, the item's images
// released to their retention window, and the room's purge.
func deleteMadeItemTx(ctx context.Context, tx *sql.Tx, kind SpaceItemKind, spaceID, actorUserID, itemID string) error {
	if err := recordMadeItemEventTx(ctx, tx, kind, spaceID, actorUserID, "deleted", itemID); err != nil {
		return err
	}
	if kind.AssetTable != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE `+kind.AssetTable+` SET lifecycle_state='unreferenced',deleted_at=COALESCE(deleted_at,NOW())
			 WHERE `+kind.ControlColumn+`=$1 AND lifecycle_state='ready'`, itemID); err != nil {
			return err
		}
	}
	return kind.enqueueControlTx(ctx, tx, itemID, "purge", nil)
}
