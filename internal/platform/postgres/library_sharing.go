package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type LibrarySharedReference struct {
	ID                   string     `json:"id"`
	GrantID              string     `json:"grant_id"`
	SourceSpaceID        string     `json:"source_space_id"`
	SourceSpaceName      string     `json:"source_space_name"`
	SourceItemID         string     `json:"source_item_id"`
	DestinationSpaceID   string     `json:"destination_space_id"`
	DestinationSpaceName string     `json:"destination_space_name"`
	DisplayName          string     `json:"display_name"`
	MIMEType             string     `json:"mime_type"`
	ByteSize             int64      `json:"byte_size"`
	State                string     `json:"state"`
	Version              int64      `json:"version"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func (db *Database) LibraryOutgoingGrants(ctx context.Context, userID, sourceSpaceID string) ([]LibrarySharedReference, error) {
	items := []LibrarySharedReference{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, sourceSpaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, librarySharedReferenceSelect+` WHERE g.source_space_id=$1 AND g.state='active' AND r.lifecycle_state='ready' AND i.lifecycle_state='ready' AND i.hidden=FALSE ORDER BY r.created_at DESC`, sourceSpaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibrarySharedReference
			if err := scanLibrarySharedReference(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CreateLibraryGrant(ctx context.Context, userID, sourceSpaceID, sourceItemID, destinationSpaceID string) (*LibrarySharedReference, error) {
	if sourceSpaceID == destinationSpaceID || sourceItemID == "" || destinationSpaceID == "" {
		return nil, ErrLibraryInvalid
	}
	grantID, referenceID := "grant_"+uuid.NewString(), "reference_"+uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, sourceSpaceID, PermissionLibraryDownload); err != nil {
			return err
		}
		if _, err := requireSpaceMemberTx(ctx, tx, destinationSpaceID, userID); err != nil {
			return ErrLibraryForbidden
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, sourceSpaceID, sourceItemID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready' AND audience_kind='space')`, sourceItemID, sourceSpaceID).Scan(&exists); err != nil || !exists {
			return ErrLibraryNotFound
		}
		if err := tx.QueryRowContext(ctx, `SELECT g.id,r.id FROM space_library_grants g JOIN space_library_direct_references r ON r.grant_id=g.id WHERE g.source_space_id=$1 AND g.source_item_id=$2 AND g.destination_space_id=$3 AND g.state='active' LIMIT 1`, sourceSpaceID, sourceItemID, destinationSpaceID).Scan(&grantID, &referenceID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_grants(id,source_space_id,source_item_id,destination_space_id,granted_by_user_id) VALUES($1,$2,$3,$4,$5)`, grantID, sourceSpaceID, sourceItemID, destinationSpaceID, userID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_direct_references(id,destination_space_id,grant_id,created_by_user_id) VALUES($1,$2,$3,$4)`, referenceID, destinationSpaceID, grantID, userID); err != nil {
				return err
			}
		}
		return insertLibraryAuditTx(ctx, tx, sourceSpaceID, "", userID, "library.grant.created", "grant", grantID, "success", map[string]any{"destination_space_id": destinationSpaceID})
	})
	if err != nil {
		return nil, err
	}
	return db.LibrarySharedReference(ctx, userID, destinationSpaceID, referenceID)
}

func (db *Database) LibrarySharedReferences(ctx context.Context, userID, destinationSpaceID string) ([]LibrarySharedReference, error) {
	items := []LibrarySharedReference{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, destinationSpaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, librarySharedReferenceSelect+` WHERE r.destination_space_id=$1 AND r.lifecycle_state='ready' AND g.state='active' AND (g.expires_at IS NULL OR g.expires_at>NOW()) AND i.lifecycle_state='ready' ORDER BY r.created_at DESC`, destinationSpaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibrarySharedReference
			if err := scanLibrarySharedReference(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) LibrarySharedReference(ctx context.Context, userID, destinationSpaceID, referenceID string) (*LibrarySharedReference, error) {
	out := &LibrarySharedReference{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, destinationSpaceID, PermissionLibraryView); err != nil {
			return err
		}
		return scanLibrarySharedReference(tx.QueryRowContext(ctx, librarySharedReferenceSelect+` WHERE r.id=$1 AND r.destination_space_id=$2 AND r.lifecycle_state='ready' AND g.state='active' AND (g.expires_at IS NULL OR g.expires_at>NOW()) AND i.lifecycle_state='ready'`, referenceID, destinationSpaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func (db *Database) LibrarySharedReferenceDownload(ctx context.Context, userID, destinationSpaceID, referenceID string) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, destinationSpaceID, PermissionLibraryDownload); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT COALESCE(rb.r2_object_key,b.r2_object_key),i.display_name,COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type),COALESCE(rb.byte_size,b.byte_size),COALESCE(rb.sha256,b.sha256),(rb.id IS NOT NULL)
			FROM space_library_direct_references r JOIN space_library_grants g ON g.id=r.grant_id
			JOIN space_library_items i ON i.id=g.source_item_id JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready'
			LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'
			WHERE r.id=$1 AND r.destination_space_id=$2 AND r.lifecycle_state='ready' AND g.state='active' AND (g.expires_at IS NULL OR g.expires_at>NOW()) AND i.lifecycle_state='ready' AND b.lifecycle_state='ready'`, referenceID, destinationSpaceID).Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256, &out.Rendition)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func (db *Database) RevokeLibraryGrant(ctx context.Context, userID, sourceSpaceID, grantID string, version int64) error {
	if version < 1 {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, sourceSpaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_grants SET state='revoked',revoked_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND source_space_id=$2 AND version=$3 AND state='active'`, grantID, sourceSpaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_direct_references SET lifecycle_state='unavailable',updated_at=NOW() WHERE grant_id=$1`, grantID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, sourceSpaceID, "", userID, "library.grant.revoked", "grant", grantID, "success", map[string]any{})
	})
}

const librarySharedReferenceSelect = `SELECT r.id,g.id,g.source_space_id,s.name,g.source_item_id,r.destination_space_id,d.name,i.display_name,COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type),COALESCE(rb.byte_size,b.byte_size),g.state,g.version,g.expires_at,r.created_at,r.updated_at FROM space_library_direct_references r JOIN space_library_grants g ON g.id=r.grant_id JOIN spaces s ON s.id=g.source_space_id JOIN spaces d ON d.id=g.destination_space_id JOIN space_library_items i ON i.id=g.source_item_id JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready' LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'`

func scanLibrarySharedReference(scanner interface{ Scan(...any) error }, out *LibrarySharedReference) error {
	return scanner.Scan(&out.ID, &out.GrantID, &out.SourceSpaceID, &out.SourceSpaceName, &out.SourceItemID, &out.DestinationSpaceID, &out.DestinationSpaceName, &out.DisplayName, &out.MIMEType, &out.ByteSize, &out.State, &out.Version, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
}
