package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) CreateLibraryAlbumFolder(ctx context.Context, userID, spaceID, parentFolderID, name string) (*LibraryAlbumFolder, error) {
	name, parentFolderID = strings.TrimSpace(name), strings.TrimSpace(parentFolderID)
	if name == "" || len([]rune(name)) > 120 {
		return nil, ErrLibraryInvalid
	}
	folderID := "album_folder_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var parent any
		if parentFolderID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_album_folders WHERE id=$1 AND space_id=$2)`, parentFolderID, spaceID).Scan(&valid); err != nil || !valid {
				return ErrLibraryInvalid
			}
			parent = parentFolderID
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_album_folders(id,space_id,parent_folder_id,name,position,created_by_user_id) VALUES($1,$2,$3,$4,(SELECT COALESCE(max(position),-1)+1 FROM space_album_folders WHERE space_id=$2 AND parent_folder_id IS NOT DISTINCT FROM $3),$5)`, folderID, spaceID, parent, name, userID)
		if err != nil {
			return mapLibraryConstraintError(err)
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album_folder.created", "album_folder", folderID, "success", map[string]any{"parent_folder_id": parentFolderID})
	})
	if err != nil {
		return nil, err
	}
	folders, err := db.LibraryAlbumFolders(ctx, userID, spaceID)
	for index := range folders {
		if folders[index].ID == folderID {
			return &folders[index], err
		}
	}
	return nil, ErrLibraryNotFound
}

func (db *Database) UpdateLibraryAlbumFolder(ctx context.Context, userID, spaceID, folderID string, version int64, parentFolderID, name string, position int64) (*LibraryAlbumFolder, error) {
	name, parentFolderID = strings.TrimSpace(name), strings.TrimSpace(parentFolderID)
	if version < 1 || position < 0 || name == "" || len([]rune(name)) > 120 || folderID == parentFolderID {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var parent any
		if parentFolderID != "" {
			var invalid bool
			if err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants AS (SELECT id FROM space_album_folders WHERE parent_folder_id=$1 UNION ALL SELECT child.id FROM space_album_folders child JOIN descendants d ON child.parent_folder_id=d.id) SELECT NOT EXISTS(SELECT 1 FROM space_album_folders WHERE id=$2 AND space_id=$3) OR EXISTS(SELECT 1 FROM descendants WHERE id=$2)`, folderID, parentFolderID, spaceID).Scan(&invalid); err != nil || invalid {
				return ErrLibraryInvalid
			}
			parent = parentFolderID
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_album_folders SET parent_folder_id=$1,name=$2,position=$3,version=version+1,updated_at=NOW() WHERE id=$4 AND space_id=$5 AND version=$6`, parent, name, position, folderID, spaceID, version)
		if err != nil {
			return mapLibraryConstraintError(err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album_folder.updated", "album_folder", folderID, "success", map[string]any{})
	})
	if err != nil {
		return nil, err
	}
	folders, err := db.LibraryAlbumFolders(ctx, userID, spaceID)
	for index := range folders {
		if folders[index].ID == folderID {
			return &folders[index], err
		}
	}
	return nil, ErrLibraryNotFound
}

func (db *Database) DeleteLibraryAlbumFolder(ctx context.Context, userID, spaceID, folderID string, version int64) error {
	if version < 1 {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_album_folders WHERE id=$1 AND space_id=$2 AND version=$3`, folderID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album_folder.deleted", "album_folder", folderID, "success", map[string]any{})
	})
}

func (db *Database) ReorderLibraryAlbumItems(ctx context.Context, userID, spaceID, albumID string, version int64, itemIDs []string) (*LibraryAlbum, error) {
	if version < 1 || len(itemIDs) < 1 || len(itemIDs) > 1000 || len(uniqueSpaceIDs(itemIDs)) != len(itemIDs) {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var currentVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM space_albums WHERE id=$1 AND space_id=$2 FOR UPDATE`, albumID, spaceID).Scan(&currentVersion); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryNotFound
			}
			return err
		}
		if currentVersion != version {
			return ErrLibraryConflict
		}
		for position, itemID := range itemIDs {
			result, err := tx.ExecContext(ctx, `UPDATE space_album_items ai SET position=$1 WHERE ai.album_id=$2 AND ai.space_library_item_id=$3 AND EXISTS(SELECT 1 FROM space_library_items i WHERE i.id=ai.space_library_item_id AND i.space_id=$4 AND i.lifecycle_state='ready' AND i.hidden=FALSE)`, position, albumID, itemID, spaceID)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrLibraryInvalid
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_albums SET version=version+1,updated_at=NOW() WHERE id=$1`, albumID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album.reordered", "album", albumID, "success", map[string]any{"count": len(itemIDs)})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryAlbum(ctx, userID, spaceID, albumID)
}

func (db *Database) AddLibraryAlbumItems(ctx context.Context, userID, spaceID, albumID string, itemIDs []string) error {
	itemIDs = uniqueSpaceIDs(itemIDs)
	if len(itemIDs) < 1 || len(itemIDs) > 200 {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_albums WHERE id=$1 AND space_id=$2)`, albumID, spaceID).Scan(&exists); err != nil || !exists {
			return ErrLibraryNotFound
		}
		for _, itemID := range itemIDs {
			result, err := tx.ExecContext(ctx, `INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id) SELECT $1,i.id,$3 FROM space_library_items i WHERE i.id=$2 AND i.space_id=$4 AND i.lifecycle_state='ready' AND i.hidden=FALSE ON CONFLICT DO NOTHING`, albumID, itemID, userID, spaceID)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				var valid bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready' AND hidden=FALSE)`, itemID, spaceID).Scan(&valid); err != nil || !valid {
					return ErrLibraryInvalid
				}
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_albums SET version=version+1,updated_at=NOW() WHERE id=$1`, albumID)
		return err
	})
}

func (db *Database) RemoveLibraryAlbumItem(ctx context.Context, userID, spaceID, albumID, itemID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_album_items ai USING space_albums a WHERE ai.album_id=a.id AND a.id=$1 AND a.space_id=$2 AND ai.space_library_item_id=$3`, albumID, spaceID, itemID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryNotFound
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_albums SET version=version+1,updated_at=NOW() WHERE id=$1`, albumID)
		return err
	})
}

func (db *Database) LibraryAlbumItems(ctx context.Context, userID, spaceID, albumID string, limit int) ([]SpaceLibraryItem, error) {
	if limit < 1 || limit > 200 {
		limit = 200
	}
	items := []SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, libraryItemSelect+` JOIN space_album_items ai ON ai.space_library_item_id=i.id JOIN space_albums a ON a.id=ai.album_id WHERE a.id=$1 AND a.space_id=$2 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND `+resourceAudienceSQL("i", "$4")+` ORDER BY CASE a.sort_mode WHEN 'oldest' THEN extract(epoch FROM COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) WHEN 'newest' THEN -extract(epoch FROM COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) ELSE ai.position::double precision END,ai.added_at DESC LIMIT $3`, albumID, spaceID, limit, userID)
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

func (db *Database) LibraryGroups(ctx context.Context, userID, spaceID string) ([]LibraryGroup, error) {
	items := []LibraryGroup{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,name,rules,created_by_user_id,version,created_at,updated_at FROM space_library_groups WHERE space_id=$1 ORDER BY lower(name),id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibraryGroup
			var raw []byte
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Name, &raw, &item.CreatedByUserID, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &item.Rules); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
