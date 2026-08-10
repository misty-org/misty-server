package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) CreateLibraryEditVersion(ctx context.Context, userID, spaceID, itemID string, itemVersion int64, definition LibraryEditDefinition) (*LibraryEditResult, error) {
	if itemVersion < 1 {
		return nil, ErrLibraryInvalid
	}
	edit := &LibraryEditVersion{ID: "edit_" + uuid.NewString(), SpaceLibraryID: itemID, CreatedByUserID: userID, Definition: definition, LifecycleState: "ready", RenditionState: "none"}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		var currentID, mimeType string
		var actualVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(i.current_edit_version_id,''),i.version,b.server_detected_mime_type FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' FOR UPDATE OF i`, itemID, spaceID).Scan(&currentID, &actualVersion, &mimeType); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		if actualVersion != itemVersion {
			return ErrLibraryConflict
		}
		if !strings.HasPrefix(mimeType, "image/") && !strings.HasPrefix(mimeType, "video/") {
			return ErrLibraryInvalid
		}
		if err := definition.Validate(mimeType); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM library_item_versions WHERE space_library_item_id=$1 AND lifecycle_state='ready'`, itemID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxLibraryEditVersions {
			return ErrLibraryInvalid
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(version_number),0)+1 FROM library_item_versions WHERE space_library_item_id=$1`, itemID).Scan(&edit.VersionNumber); err != nil {
			return err
		}
		edit.ParentVersionID = currentID
		raw, _ := json.Marshal(definition)
		var parent any
		if currentID != "" {
			parent = currentID
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO library_item_versions(id,space_library_item_id,parent_version_id,created_by_user_id,edit_definition,version_number) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at,rendition_updated_at`, edit.ID, itemID, parent, userID, raw, edit.VersionNumber).Scan(&edit.CreatedAt, &edit.RenditionAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_items SET current_edit_version_id=$1,version=version+1,updated_at=NOW() WHERE id=$2`, edit.ID, itemID); err != nil {
			return err
		}
		edit.IsCurrent = true
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.edit.created", "edit", edit.ID, "success", map[string]any{"version_number": edit.VersionNumber})
	})
	if err != nil {
		return nil, err
	}
	item, err := db.LibraryItem(ctx, userID, spaceID, itemID)
	if err != nil {
		return nil, err
	}
	return &LibraryEditResult{Item: item, Edit: edit}, nil
}

func (db *Database) SelectLibraryEditVersion(ctx context.Context, userID, spaceID, itemID, editID string, itemVersion int64) (*LibraryEditResult, error) {
	if itemVersion < 1 {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		if editID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_item_versions WHERE id=$1 AND space_library_item_id=$2 AND lifecycle_state='ready')`, editID, itemID).Scan(&valid); err != nil || !valid {
				return ErrLibraryNotFound
			}
		}
		var selected any
		if editID != "" {
			selected = editID
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_items SET current_edit_version_id=$1,version=version+1,updated_at=NOW() WHERE id=$2 AND space_id=$3 AND version=$4 AND lifecycle_state='ready'`, selected, itemID, spaceID, itemVersion)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.edit.selected", "edit", editID, "success", map[string]any{"original": editID == ""})
	})
	if err != nil {
		return nil, err
	}
	item, err := db.LibraryItem(ctx, userID, spaceID, itemID)
	if err != nil {
		return nil, err
	}
	return &LibraryEditResult{Item: item}, nil
}

func (db *Database) DeleteLibraryEditVersion(ctx context.Context, userID, spaceID, itemID, editID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		var currentID, renditionState, domainID string
		var editVersionNumber int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(current_edit_version_id,'') FROM space_library_items WHERE id=$1 AND space_id=$2 FOR UPDATE`, itemID, spaceID).Scan(&currentID); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		if currentID == editID {
			return ErrLibraryConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT v.rendition_state,v.version_number,f.security_domain_id FROM library_item_versions v JOIN space_library_items i ON i.id=v.space_library_item_id JOIN library_files f ON f.id=i.file_id WHERE v.id=$1 AND v.space_library_item_id=$2 AND v.lifecycle_state='ready' FOR UPDATE OF v`, editID, itemID).Scan(&renditionState, &editVersionNumber, &domainID); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		var released int64
		if err := tx.QueryRowContext(ctx, `UPDATE space_rendition_reservations SET state='released',updated_at=NOW() WHERE source_kind='edit' AND source_id=$1 AND state='active' RETURNING reserved_bytes`, editID).Scan(&released); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if released > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
				return err
			}
		}
		if renditionState == "queued" || renditionState == "processing" {
			if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='canceled',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,error_code='edit_deleted',updated_at=NOW() WHERE job_kind='edit' AND target_id=$1 AND state IN ('queued','leased','running')`, editID); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET lifecycle_state='recovery',deleted_at=NOW() WHERE id=$1 AND space_library_item_id=$2 AND lifecycle_state='ready'`, editID, itemID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='recovery',updated_at=NOW() WHERE space_id=$1 AND source_kind='edit' AND source_id=$2 AND state='active'`, spaceID, editID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_recovery_tombstones(id,security_domain_id,space_id,target_kind,target_id,recover_until,target_version)
			VALUES($1,$2,$3,'edit',$4,NOW()+$5::interval,$6)
			ON CONFLICT(target_kind,target_id) DO UPDATE SET lifecycle_state='recovery',recover_until=EXCLUDED.recover_until,target_version=EXCLUDED.target_version,delete_lease_token=NULL,delete_lease_expires_at=NULL,updated_at=NOW()`, "tombstone_"+uuid.NewString(), domainID, spaceID, editID, LibraryRecoveryWindow.String(), editVersionNumber); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.edit.deleted", "edit", editID, "success", map[string]any{})
	})
}
