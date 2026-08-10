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
	MaxLibraryAlbums     = 500
	MaxLibraryGroups     = 100
	MaxLibraryGroupRules = 12
)

type LibraryAlbum struct {
	ID              string    `json:"id"`
	SpaceID         string    `json:"space_id"`
	FolderID        string    `json:"folder_id,omitempty"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	CoverItemID     string    `json:"cover_item_id,omitempty"`
	Position        int64     `json:"position"`
	ViewMode        string    `json:"view_mode"`
	SortMode        string    `json:"sort_mode"`
	CreatedByUserID string    `json:"created_by_user_id"`
	ItemCount       int       `json:"item_count"`
	Version         int64     `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type LibraryAlbumFolder struct {
	ID              string    `json:"id"`
	SpaceID         string    `json:"space_id"`
	ParentFolderID  string    `json:"parent_folder_id,omitempty"`
	Name            string    `json:"name"`
	Position        int64     `json:"position"`
	AlbumCount      int       `json:"album_count"`
	FolderCount     int       `json:"folder_count"`
	CreatedByUserID string    `json:"created_by_user_id"`
	Version         int64     `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const libraryAlbumSelect = `SELECT a.id,a.space_id,COALESCE(a.folder_id,''),a.name,a.description,COALESCE(CASE WHEN EXISTS(SELECT 1 FROM space_library_items cover WHERE cover.id=a.cover_item_id AND cover.lifecycle_state='ready' AND cover.hidden=FALSE) THEN a.cover_item_id END,''),a.position,a.view_mode,a.sort_mode,a.created_by_user_id,(SELECT count(*) FROM space_album_items ai JOIN space_library_items i ON i.id=ai.space_library_item_id WHERE ai.album_id=a.id AND i.lifecycle_state='ready' AND i.hidden=FALSE),a.version,a.created_at,a.updated_at FROM space_albums a`

type LibraryGroupRule struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

type LibraryGroupRules struct {
	All []LibraryGroupRule `json:"all"`
}

type LibraryGroup struct {
	ID              string            `json:"id"`
	SpaceID         string            `json:"space_id"`
	Name            string            `json:"name"`
	Rules           LibraryGroupRules `json:"rules"`
	CreatedByUserID string            `json:"created_by_user_id"`
	Version         int64             `json:"version"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

func (db *Database) LibraryAlbums(ctx context.Context, userID, spaceID string) ([]LibraryAlbum, error) {
	items := []LibraryAlbum{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, libraryAlbumSelect+` WHERE a.space_id=$1 ORDER BY a.position,lower(a.name),a.id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibraryAlbum
			if err := scanLibraryAlbum(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CreateLibraryAlbum(ctx context.Context, userID, spaceID, name, description string) (*LibraryAlbum, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 120 || len([]rune(description)) > 2000 {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryAlbum{ID: "album_" + uuid.NewString(), SpaceID: spaceID, Name: name, Description: description, CreatedByUserID: userID, Version: 1}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_albums WHERE space_id=$1`, spaceID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxLibraryAlbums {
			return ErrLibraryInvalid
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_albums(id,space_id,name,description,created_by_user_id) VALUES($1,$2,$3,$4,$5) RETURNING created_at,updated_at`, out.ID, spaceID, name, description, userID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album.created", "album", out.ID, "success", map[string]any{})
	})
	if err != nil {
		return nil, mapLibraryConstraintError(err)
	}
	return db.LibraryAlbum(ctx, userID, spaceID, out.ID)
}

func (db *Database) LibraryAlbum(ctx context.Context, userID, spaceID, albumID string) (*LibraryAlbum, error) {
	out := &LibraryAlbum{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		return scanLibraryAlbum(tx.QueryRowContext(ctx, libraryAlbumSelect+` WHERE a.id=$1 AND a.space_id=$2`, albumID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func (db *Database) UpdateLibraryAlbum(ctx context.Context, userID, spaceID, albumID string, version int64, name, description, coverItemID string) (*LibraryAlbum, error) {
	name, description, coverItemID = strings.TrimSpace(name), strings.TrimSpace(description), strings.TrimSpace(coverItemID)
	if version < 1 || name == "" || len([]rune(name)) > 120 || len([]rune(description)) > 2000 {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if coverItemID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_album_items ai JOIN space_albums a ON a.id=ai.album_id JOIN space_library_items i ON i.id=ai.space_library_item_id WHERE a.id=$1 AND a.space_id=$2 AND ai.space_library_item_id=$3 AND i.lifecycle_state='ready' AND i.hidden=FALSE)`, albumID, spaceID, coverItemID).Scan(&valid); err != nil || !valid {
				return ErrLibraryInvalid
			}
		}
		var cover any
		if coverItemID != "" {
			cover = coverItemID
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_albums SET name=$1,description=$2,cover_item_id=$3,version=version+1,updated_at=NOW() WHERE id=$4 AND space_id=$5 AND version=$6`, name, description, cover, albumID, spaceID, version)
		if err != nil {
			return mapLibraryConstraintError(err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album.updated", "album", albumID, "success", map[string]any{"cover_changed": true})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryAlbum(ctx, userID, spaceID, albumID)
}

func (db *Database) DeleteLibraryAlbum(ctx context.Context, userID, spaceID, albumID string, version int64) error {
	if version < 1 {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_albums WHERE id=$1 AND space_id=$2 AND version=$3`, albumID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album.deleted", "album", albumID, "success", map[string]any{})
	})
}

func (db *Database) OrganizeLibraryAlbum(ctx context.Context, userID, spaceID, albumID string, version int64, folderID, viewMode, sortMode string, position int64) (*LibraryAlbum, error) {
	folderID, viewMode, sortMode = strings.TrimSpace(folderID), strings.TrimSpace(viewMode), strings.TrimSpace(sortMode)
	if version < 1 || position < 0 || viewMode != "grid" && viewMode != "list" || sortMode != "custom" && sortMode != "oldest" && sortMode != "newest" {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var folder any
		if folderID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_album_folders WHERE id=$1 AND space_id=$2)`, folderID, spaceID).Scan(&valid); err != nil || !valid {
				return ErrLibraryInvalid
			}
			folder = folderID
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_albums SET folder_id=$1,view_mode=$2,sort_mode=$3,position=$4,version=version+1,updated_at=NOW() WHERE id=$5 AND space_id=$6 AND version=$7`, folder, viewMode, sortMode, position, albumID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.album.organized", "album", albumID, "success", map[string]any{"folder_id": folderID, "view_mode": viewMode, "sort_mode": sortMode, "position": position})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryAlbum(ctx, userID, spaceID, albumID)
}

func (db *Database) LibraryAlbumFolders(ctx context.Context, userID, spaceID string) ([]LibraryAlbumFolder, error) {
	folders := []LibraryAlbumFolder{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT f.id,f.space_id,COALESCE(f.parent_folder_id,''),f.name,f.position,(SELECT count(*) FROM space_albums a WHERE a.folder_id=f.id),(SELECT count(*) FROM space_album_folders child WHERE child.parent_folder_id=f.id),f.created_by_user_id,f.version,f.created_at,f.updated_at FROM space_album_folders f WHERE f.space_id=$1 ORDER BY f.position,lower(f.name),f.id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var folder LibraryAlbumFolder
			if err := rows.Scan(&folder.ID, &folder.SpaceID, &folder.ParentFolderID, &folder.Name, &folder.Position, &folder.AlbumCount, &folder.FolderCount, &folder.CreatedByUserID, &folder.Version, &folder.CreatedAt, &folder.UpdatedAt); err != nil {
				return err
			}
			folders = append(folders, folder)
		}
		return rows.Err()
	})
	return folders, err
}
