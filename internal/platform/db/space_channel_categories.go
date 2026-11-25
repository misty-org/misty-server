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

type SpaceChannelCategory struct {
	ID        string    `json:"id"`
	SpaceID   string    `json:"space_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func normalizeChannelCategoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", ErrSpaceInvalid
	}
	return name, nil
}

func requireSpaceChannelCategoryTx(ctx context.Context, tx *sql.Tx, spaceID, categoryID string) error {
	if categoryID == "" {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_channel_categories WHERE id=$1 AND space_id=$2)`,
		categoryID, spaceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrSpaceInvalid
	}
	return nil
}

func requireChannelsManageTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return err
	}
	if !access.base.Has(PermissionChannelsManage) {
		return ErrSpaceForbidden
	}
	return nil
}

func lockChannelLayoutTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:channel-layout:"+spaceID)
	return err
}

func recordChannelCategoryEventTx(ctx context.Context, tx *sql.Tx, spaceID, userID, eventType, categoryID string) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, eventType, categoryID, map[string]any{"category_id": categoryID})
	return err
}

func spaceChannelCategoriesTx(ctx context.Context, tx *sql.Tx, spaceID string) ([]SpaceChannelCategory, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,space_id,name,position,created_at,updated_at
		FROM space_channel_categories WHERE space_id=$1 ORDER BY position,created_at,id`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SpaceChannelCategory{}
	for rows.Next() {
		var item SpaceChannelCategory
		if err := rows.Scan(&item.ID, &item.SpaceID, &item.Name, &item.Position, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadSpaceChannelCategoryTx(ctx context.Context, tx *sql.Tx, spaceID, categoryID string) (*SpaceChannelCategory, error) {
	out := &SpaceChannelCategory{}
	err := tx.QueryRowContext(ctx, `SELECT id,space_id,name,position,created_at,updated_at
		FROM space_channel_categories WHERE id=$1 AND space_id=$2`, categoryID, spaceID).
		Scan(&out.ID, &out.SpaceID, &out.Name, &out.Position, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) SpaceChannelCategories(ctx context.Context, userID, spaceID string) ([]SpaceChannelCategory, error) {
	var items []SpaceChannelCategory
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var err error
		items, err = spaceChannelCategoriesTx(ctx, tx, spaceID)
		return err
	})
	return items, err
}

func (db *Database) CreateSpaceChannelCategory(ctx context.Context, userID, spaceID, name string) (*SpaceChannelCategory, error) {
	name, err := normalizeChannelCategoryName(name)
	if err != nil {
		return nil, err
	}
	out := &SpaceChannelCategory{ID: "space_category_" + uuid.NewString(), SpaceID: spaceID, Name: name}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireChannelsManageTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := lockChannelLayoutTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_channel_categories(id,space_id,name,position)
			VALUES($1,$2,$3,(SELECT COALESCE(max(position)+1,0) FROM space_channel_categories WHERE space_id=$2))
			RETURNING position,created_at,updated_at`, out.ID, spaceID, name).
			Scan(&out.Position, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		return recordChannelCategoryEventTx(ctx, tx, spaceID, userID, "channel_category.created", out.ID)
	})
	return out, err
}

type SpaceChannelCategoryChange struct {
	Name    *string
	AfterID *string
}

func (db *Database) UpdateSpaceChannelCategory(ctx context.Context, userID, spaceID, categoryID string, change SpaceChannelCategoryChange) (*SpaceChannelCategory, error) {
	var name string
	if change.Name != nil {
		normalized, err := normalizeChannelCategoryName(*change.Name)
		if err != nil {
			return nil, err
		}
		name = normalized
	}
	var out *SpaceChannelCategory
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireChannelsManageTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := lockChannelLayoutTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if _, err := loadSpaceChannelCategoryTx(ctx, tx, spaceID, categoryID); err != nil {
			return err
		}
		if change.Name != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE space_channel_categories SET name=$1,updated_at=NOW() WHERE id=$2 AND space_id=$3`,
				name, categoryID, spaceID); err != nil {
				return err
			}
		}
		if change.AfterID != nil {
			categories, err := spaceChannelCategoriesTx(ctx, tx, spaceID)
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(categories))
			for _, category := range categories {
				ids = append(ids, category.ID)
			}
			ordered, ok := placeAfter(ids, categoryID, *change.AfterID)
			if !ok {
				return ErrSpaceInvalid
			}
			for position, id := range ordered {
				if _, err := tx.ExecContext(ctx, `UPDATE space_channel_categories SET position=$1,updated_at=NOW()
					WHERE id=$2 AND space_id=$3 AND position<>$1`, position, id, spaceID); err != nil {
					return err
				}
			}
		}
		var err error
		if out, err = loadSpaceChannelCategoryTx(ctx, tx, spaceID, categoryID); err != nil {
			return err
		}
		return recordChannelCategoryEventTx(ctx, tx, spaceID, userID, "channel_category.updated", categoryID)
	})
	return out, err
}

func (db *Database) DeleteSpaceChannelCategory(ctx context.Context, userID, spaceID, categoryID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireChannelsManageTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := lockChannelLayoutTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if _, err := loadSpaceChannelCategoryTx(ctx, tx, spaceID, categoryID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversations c SET category_id=NULL,position=moved.n
			FROM (SELECT id,(SELECT COALESCE(max(position)+1,0) FROM space_conversations
			        WHERE space_id=$1 AND kind='standard' AND category_id IS NULL)
			        + (row_number() OVER (ORDER BY position,created_at,id))::integer - 1 AS n
			      FROM space_conversations WHERE space_id=$1 AND category_id=$2) moved
			WHERE c.id=moved.id`, spaceID, categoryID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_channel_categories WHERE id=$1 AND space_id=$2`, categoryID, spaceID); err != nil {
			return err
		}
		return recordChannelCategoryEventTx(ctx, tx, spaceID, userID, "channel_category.deleted", categoryID)
	})
}

func (db *Database) PlaceSpaceChannel(ctx context.Context, userID, spaceID, conversationID, categoryID, afterID string) (*SpaceConversation, error) {
	var out *SpaceConversation
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := memberAccessTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		if err := lockChannelLayoutTx(ctx, tx, spaceID); err != nil {
			return err
		}
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM space_conversations WHERE id=$1 AND space_id=$2 FOR UPDATE`,
			conversationID, spaceID).Scan(&kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		permissions, _, err := channelPermissionsForTx(ctx, tx, access, userID, spaceID, conversationID)
		if err != nil {
			return err
		}
		if !permissions.Has(PermissionMessagesRead) {
			return ErrSpaceNotFound
		}
		if kind != "standard" || !permissions.Has(PermissionChannelsManage) {
			return ErrSpaceForbidden
		}
		if err := requireSpaceChannelCategoryTx(ctx, tx, spaceID, categoryID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM space_conversations
			WHERE space_id=$1 AND kind='standard' AND category_id IS NOT DISTINCT FROM NULLIF($2,'') AND id<>$3
			ORDER BY position,created_at,id`, spaceID, categoryID, conversationID)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		ordered, ok := placeAfter(append(ids, conversationID), conversationID, afterID)
		if !ok {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversations SET category_id=NULLIF($1,'') WHERE id=$2 AND space_id=$3`,
			categoryID, conversationID, spaceID); err != nil {
			return err
		}
		for position, id := range ordered {
			if _, err := tx.ExecContext(ctx, `UPDATE space_conversations SET position=$1
				WHERE id=$2 AND space_id=$3 AND position<>$1`, position, id, spaceID); err != nil {
				return err
			}
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID,
			map[string]any{"conversation_id": conversationID, "placed": true}); err != nil {
			return err
		}
		if out, err = loadSpaceConversationTx(ctx, tx, spaceID, conversationID); err != nil {
			return err
		}
		return fillConversationAccessTx(ctx, tx, access, out)
	})
	return out, err
}

func placeAfter(ids []string, moving, afterID string) ([]string, bool) {
	rest := make([]string, 0, len(ids))
	found := false
	for _, id := range ids {
		if id == moving {
			found = true
			continue
		}
		rest = append(rest, id)
	}
	if !found || afterID == moving {
		return nil, false
	}
	if afterID == "" {
		return append([]string{moving}, rest...), true
	}
	for index, id := range rest {
		if id == afterID {
			out := make([]string, 0, len(ids))
			out = append(out, rest[:index+1]...)
			out = append(out, moving)
			return append(out, rest[index+1:]...), true
		}
	}
	return nil, false
}
