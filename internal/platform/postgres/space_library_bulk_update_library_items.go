package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) BulkUpdateLibraryItems(ctx context.Context, userID, spaceID string, operation BulkLibraryItemOperation) ([]SpaceLibraryItem, error) {
	versions, ids, err := validateBulkLibraryItemRefs(operation.Items)
	if err != nil {
		return nil, err
	}
	if err := validateBulkLibraryOperation(&operation); err != nil {
		return nil, err
	}

	items := []SpaceLibraryItem{}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := authorizeBulkLibraryItemsTx(ctx, tx, userID, spaceID, ids); err != nil {
			return err
		}
		states, err := lockBulkLibraryItemStatesTx(ctx, tx, spaceID, ids, versions)
		if err != nil {
			return err
		}
		if err := requireBulkLibraryItemStates(operation.Action, ids, states); err != nil {
			return err
		}
		if err := applyBulkLibraryItemMutationTx(ctx, tx, userID, spaceID, operation, ids); err != nil {
			return err
		}
		if err := recordBulkLibraryAuditTx(ctx, tx, spaceID, userID, operation, ids); err != nil {
			return err
		}
		return loadBulkLibraryItemsTx(ctx, tx, spaceID, ids, &items)
	})
	return items, err
}

func (db *Database) UpdateLibraryItem(ctx context.Context, userID, spaceID, itemID string, version int64, displayName, caption string, tags []string, favorite, hidden bool) (*SpaceLibraryItem, error) {
	displayName = strings.TrimSpace(displayName)
	caption = strings.TrimSpace(caption)
	if displayName == "" || len([]rune(displayName)) > 255 || len([]rune(caption)) > 4000 || len(tags) > 100 {
		return nil, ErrLibraryInvalid
	}
	encodedTags, _ := json.Marshal(normalizeLibraryTags(tags))
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_items SET display_name=$1,caption=$2,tags=$3,favorite=$4,hidden=$5,version=version+1,updated_at=NOW() WHERE id=$6 AND space_id=$7 AND version=$8 AND lifecycle_state='ready'`, displayName, caption, encodedTags, favorite, hidden, itemID, spaceID, version)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.item.updated", "library_item", itemID, "success", map[string]any{"version": version + 1})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryItem(ctx, userID, spaceID, itemID)
}

// SetLibraryImportProvenance records the external source of an item imported
// through a device-local provider. It is separate from ordinary metadata edits
// so members need library.import (and must own the freshly uploaded item), not
// the broader library.edit permission.
func (db *Database) SetLibraryImportProvenance(ctx context.Context, userID, spaceID, itemID string, provenance map[string]any) (*SpaceLibraryItem, error) {
	encoded, err := json.Marshal(map[string]any{"import_source": provenance})
	if err != nil || len(encoded) > 8192 {
		return nil, ErrLibraryInvalid
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryImport); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_items
			SET contributor_information=$1,version=version+1,updated_at=NOW()
			WHERE id=$2 AND space_id=$3 AND contributing_user_id=$4 AND lifecycle_state='ready'`,
			encoded, itemID, spaceID, userID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrLibraryNotFound
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.item.imported_from_provider",
			"library_item", itemID, "success", provenance)
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryItem(ctx, userID, spaceID, itemID)
}

func (db *Database) LibraryItem(ctx context.Context, userID, spaceID, itemID string) (*SpaceLibraryItem, error) {
	out := &SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		return scanSpaceLibraryItem(tx.QueryRowContext(ctx, `SELECT i.id,i.space_id,i.file_id,i.contributing_user_id,i.display_name,i.caption,i.tags,i.favorite,i.hidden,i.date_override,COALESCE(i.location_override,'null'::jsonb),i.contributor_information,COALESCE(i.current_edit_version_id,''),i.added_by_user_id,i.lifecycle_state,i.added_at,i.trashed_at,i.recover_until,i.version,i.updated_at,
			f.id,f.blob_id,f.security_domain_id,f.uploader_user_id,f.original_filename,f.intrinsic_metadata,f.lifecycle_state,f.original_uploaded_at,f.version FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1 AND i.space_id=$2`, itemID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func (db *Database) PromoteMessageAttachment(ctx context.Context, userID, spaceID, attachmentID string) (*SpaceLibraryItem, error) {
	item := &SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryAdd); err != nil {
			return err
		}
		var attachment MessageAttachment
		if err := scanMessageAttachment(tx.QueryRowContext(ctx, `SELECT id,space_id,COALESCE(message_id,''),file_id,upload_id,uploader_user_id,display_name,COALESCE(promoted_item_id,''),lifecycle_state,created_at,deleted_at,recover_until FROM space_message_attachments WHERE id=$1 AND space_id=$2 FOR UPDATE`, attachmentID, spaceID), &attachment); err != nil {
			return err
		}
		if attachment.PromotedItemID != "" {
			return scanSpaceLibraryItem(tx.QueryRowContext(ctx, libraryItemSelect+` WHERE i.id=$1 AND i.space_id=$2`, attachment.PromotedItemID, spaceID), item)
		}
		item.ID, item.SpaceID, item.FileID, item.ContributingUserID, item.DisplayName, item.AddedByUserID, item.LifecycleState, item.Version = "item_"+uuid.NewString(), spaceID, attachment.FileID, attachment.UploaderUserID, attachment.DisplayName, userID, "ready", 1
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_library_items(id,space_id,file_id,contributing_user_id,display_name,added_by_user_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING added_at,updated_at`, item.ID, spaceID, attachment.FileID, attachment.UploaderUserID, attachment.DisplayName, userID).Scan(&item.AddedAt, &item.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_message_attachments SET promoted_item_id=$1 WHERE id=$2`, item.ID, attachment.ID); err != nil {
			return err
		}
		if err := insertDefaultAliasTx(ctx, tx, spaceID, "library_item", item.ID, userID); err != nil {
			return err
		}
		if err := scanLibraryFile(tx.QueryRowContext(ctx, `SELECT id,blob_id,security_domain_id,uploader_user_id,original_filename,intrinsic_metadata,lifecycle_state,original_uploaded_at,version FROM library_files WHERE id=$1`, item.FileID), &item.File); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, item.File.SecurityDomainID, userID, "library.item.promoted", "library_item", item.ID, "success", map[string]any{"attachment_id": attachment.ID})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return item, err
}
