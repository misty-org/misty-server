package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) CreateLibraryGroup(ctx context.Context, userID, spaceID, name string, rules LibraryGroupRules) (*LibraryGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 120 || validateLibraryGroupRules(rules) != nil {
		return nil, ErrLibraryInvalid
	}
	raw, _ := json.Marshal(rules)
	out := &LibraryGroup{ID: "group_" + uuid.NewString(), SpaceID: spaceID, Name: name, Rules: rules, CreatedByUserID: userID, Version: 1}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_library_groups WHERE space_id=$1`, spaceID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxLibraryGroups {
			return ErrLibraryInvalid
		}
		return tx.QueryRowContext(ctx, `INSERT INTO space_library_groups(id,space_id,name,rules,created_by_user_id) VALUES($1,$2,$3,$4,$5) RETURNING created_at,updated_at`, out.ID, spaceID, name, raw, userID).Scan(&out.CreatedAt, &out.UpdatedAt)
	})
	return out, mapLibraryConstraintError(err)
}

func (db *Database) LibraryGroupItems(ctx context.Context, userID, spaceID, groupID string, limit int) ([]SpaceLibraryItem, error) {
	if limit < 1 || limit > 200 {
		limit = 200
	}
	items := []SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT rules FROM space_library_groups WHERE id=$1 AND space_id=$2`, groupID, spaceID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		var rules LibraryGroupRules
		if json.Unmarshal(raw, &rules) != nil || validateLibraryGroupRules(rules) != nil {
			return ErrLibraryInvalid
		}
		conditions := []string{"i.space_id=$1", "i.lifecycle_state='ready'", "i.hidden=FALSE", resourceAudienceSQL("i", "$2")}
		args := []any{spaceID, userID}
		for _, rule := range rules.All {
			args = append(args, rule.Value)
			placeholder := fmt.Sprintf("$%d", len(args))
			switch rule.Field {
			case "favorite":
				conditions = append(conditions, "i.favorite="+placeholder+"::boolean")
			case "hidden":
				conditions = append(conditions, "i.hidden="+placeholder+"::boolean")
			case "tag":
				conditions = append(conditions, "i.tags ? "+placeholder+"::text")
			case "mime":
				conditions = append(conditions, "f.intrinsic_metadata->>'server_detected_mime_type' LIKE "+placeholder+"::text||'%'")
			case "filename":
				conditions = append(conditions, "i.display_name ILIKE '%'||"+placeholder+"::text||'%'")
			case "album":
				conditions = append(conditions, "EXISTS(SELECT 1 FROM space_album_items ai JOIN space_albums a ON a.id=ai.album_id WHERE ai.space_library_item_id=i.id AND a.id="+placeholder+"::text AND a.space_id=i.space_id)")
			}
		}
		args = append(args, limit)
		query := libraryItemSelect + ` WHERE ` + strings.Join(conditions, " AND ") + fmt.Sprintf(" ORDER BY i.added_at DESC,i.id DESC LIMIT $%d", len(args))
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceLibraryItem
			if err := scanSpaceLibraryItem(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func validateLibraryGroupRules(rules LibraryGroupRules) error {
	if len(rules.All) > MaxLibraryGroupRules {
		return ErrLibraryInvalid
	}
	for _, rule := range rules.All {
		switch rule.Field {
		case "favorite", "hidden":
			if rule.Op != "is" {
				return ErrLibraryInvalid
			}
			if _, ok := rule.Value.(bool); !ok {
				return ErrLibraryInvalid
			}
		case "tag", "mime", "filename", "album":
			if rule.Op != "contains" && !(rule.Field == "album" && rule.Op == "in") && !(rule.Field == "mime" && rule.Op == "prefix") {
				return ErrLibraryInvalid
			}
			value, ok := rule.Value.(string)
			if !ok || strings.TrimSpace(value) == "" || len(value) > 255 {
				return ErrLibraryInvalid
			}
		default:
			return ErrLibraryInvalid
		}
	}
	return nil
}

func scanLibraryAlbum(scanner interface{ Scan(...any) error }, out *LibraryAlbum) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.FolderID, &out.Name, &out.Description, &out.CoverItemID, &out.Position, &out.ViewMode, &out.SortMode, &out.CreatedByUserID, &out.ItemCount, &out.Version, &out.CreatedAt, &out.UpdatedAt)
}

func mapLibraryConstraintError(err error) error {
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return ErrLibraryConflict
	}
	return err
}
