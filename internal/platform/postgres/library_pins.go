package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type LibraryPinnedCollection struct {
	ID             string    `json:"id"`
	SpaceID        string    `json:"space_id"`
	TargetKind     string    `json:"target_kind"`
	TargetID       string    `json:"target_id"`
	Position       int       `json:"position"`
	PinnedByUserID string    `json:"pinned_by_user_id"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type LibraryPinTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (db *Database) LibraryPinnedCollections(ctx context.Context, userID, spaceID string) ([]LibraryPinnedCollection, error) {
	out := []LibraryPinnedCollection{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,target_kind,target_id,position,pinned_by_user_id,version,created_at,updated_at FROM space_pinned_collections WHERE space_id=$1 ORDER BY position`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var pin LibraryPinnedCollection
			if err := rows.Scan(&pin.ID, &pin.SpaceID, &pin.TargetKind, &pin.TargetID, &pin.Position, &pin.PinnedByUserID, &pin.Version, &pin.CreatedAt, &pin.UpdatedAt); err != nil {
				return err
			}
			out = append(out, pin)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) SetLibraryPinnedCollections(ctx context.Context, userID, spaceID string, targets []LibraryPinTarget) ([]LibraryPinnedCollection, error) {
	if len(targets) > 12 {
		return nil, ErrLibraryInvalid
	}
	seen := map[string]bool{}
	for index := range targets {
		targets[index].Kind = strings.TrimSpace(targets[index].Kind)
		targets[index].ID = strings.TrimSpace(targets[index].ID)
		key := targets[index].Kind + "\x00" + targets[index].ID
		if targets[index].ID == "" || len([]rune(targets[index].ID)) > 255 || seen[key] {
			return nil, ErrLibraryInvalid
		}
		seen[key] = true
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		for _, target := range targets {
			valid, err := validLibraryPinTargetTx(ctx, tx, spaceID, target)
			if err != nil {
				return err
			}
			if !valid {
				return ErrLibraryNotFound
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_pinned_collections WHERE space_id=$1`, spaceID); err != nil {
			return err
		}
		for position, target := range targets {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_pinned_collections(id,space_id,target_kind,target_id,position,pinned_by_user_id) VALUES($1,$2,$3,$4,$5,$6)`, "pin_"+uuid.NewString(), spaceID, target.Kind, target.ID, position, userID); err != nil {
				return err
			}
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.pins.updated", "pinned_collections", "", "success", map[string]any{"count": len(targets)})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPinnedCollections(ctx, userID, spaceID)
}

func validLibraryPinTargetTx(ctx context.Context, tx *sql.Tx, spaceID string, target LibraryPinTarget) (bool, error) {
	switch target.Kind {
	case "system":
		return map[string]bool{"recent": true, "months": true, "years": true, "recent-days": true, "favorites": true, "featured": true, "people": true, "albums": true, "groups": true, "map": true, "shared": true, "imports": true, "recently-viewed": true, "recently-edited": true, "recently-shared": true, "recently-saved": true, "recovered": true, "screenshots": true, "documents": true, "receipts": true, "handwriting": true, "illustrations": true, "qr-codes": true, "image": true, "video": true, "audio": true, "document": true, "selfies": true, "live-photos": true, "portraits": true, "panoramas": true, "slo-mo": true, "cinematic": true, "bursts": true, "raw": true, "screen-recordings": true, "spatial": true, "hidden": true, "deleted": true}[target.ID], nil
	case "album":
		return existsPinTarget(ctx, tx, `SELECT EXISTS(SELECT 1 FROM space_albums WHERE id=$1 AND space_id=$2)`, target.ID, spaceID)
	case "group":
		return existsPinTarget(ctx, tx, `SELECT EXISTS(SELECT 1 FROM space_library_groups WHERE id=$1 AND space_id=$2)`, target.ID, spaceID)
	case "person":
		return existsPinTarget(ctx, tx, `SELECT EXISTS(SELECT 1 FROM space_people WHERE id=$1 AND space_id=$2 AND lifecycle_state='active')`, target.ID, spaceID)
	case "memory":
		if !libraryMemoryIDPattern.MatchString(target.ID) {
			return false, nil
		}
		start, err := time.Parse("2006-01", target.ID)
		if err != nil {
			return false, nil
		}
		return existsPinTarget(ctx, tx, `SELECT EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=$2 AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<$3)`, spaceID, start, start.AddDate(0, 1, 0))
	case "trip":
		return existsPinTarget(ctx, tx, `SELECT EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND lower(COALESCE(COALESCE(i.location_override,f.intrinsic_location)->>'name',''))=lower($2))`, spaceID, target.ID)
	case "map":
		if !libraryMapIDPattern.MatchString(target.ID) {
			return false, nil
		}
		coordinates := strings.Split(target.ID, ",")
		return existsPinTarget(ctx, tx, `SELECT EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND jsonb_typeof(COALESCE(i.location_override,f.intrinsic_location)->'latitude')='number' AND jsonb_typeof(COALESCE(i.location_override,f.intrinsic_location)->'longitude')='number' AND round((COALESCE(i.location_override,f.intrinsic_location)->>'latitude')::numeric,2)=round($2::numeric,2) AND round((COALESCE(i.location_override,f.intrinsic_location)->>'longitude')::numeric,2)=round($3::numeric,2))`, spaceID, coordinates[0], coordinates[1])
	default:
		return false, nil
	}
}

func existsPinTarget(ctx context.Context, tx *sql.Tx, query string, args ...any) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, query, args...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists, err
}
