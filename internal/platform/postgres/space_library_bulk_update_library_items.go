package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func (db *Database) BulkUpdateLibraryItems(ctx context.Context, userID, spaceID string, operation BulkLibraryItemOperation) ([]SpaceLibraryItem, error) {
	if len(operation.Items) < 1 || len(operation.Items) > 200 {
		return nil, ErrLibraryInvalid
	}
	versions := make(map[string]int64, len(operation.Items))
	ids := make([]string, 0, len(operation.Items))
	for _, item := range operation.Items {
		if strings.TrimSpace(item.ID) == "" || item.Version < 1 {
			return nil, ErrLibraryInvalid
		}
		if _, duplicate := versions[item.ID]; duplicate {
			return nil, ErrLibraryInvalid
		}
		versions[item.ID] = item.Version
		ids = append(ids, item.ID)
	}
	allowedAction := map[string]bool{
		"favorite": true, "unfavorite": true, "hide": true, "unhide": true,
		"trash": true, "restore": true, "add_to_album": true, "remove_from_album": true,
		"add_tags": true, "remove_tags": true, "set_date": true, "clear_date": true,
		"set_location": true, "clear_location": true,
	}
	if !allowedAction[operation.Action] || (operation.Action == "add_to_album" || operation.Action == "remove_from_album") && operation.AlbumID == "" {
		return nil, ErrLibraryInvalid
	}
	if operation.Action == "add_tags" || operation.Action == "remove_tags" {
		operation.Tags = normalizeLibraryTags(operation.Tags)
		if len(operation.Tags) < 1 || len(operation.Tags) > 100 {
			return nil, ErrLibraryInvalid
		}
	}
	if operation.Action == "set_date" && operation.DateOverride == nil {
		return nil, ErrLibraryInvalid
	}
	if operation.Action == "set_location" {
		var location map[string]any
		if len(operation.LocationOverride) < 2 || len(operation.LocationOverride) > 4096 || json.Unmarshal(operation.LocationOverride, &location) != nil || len(location) == 0 {
			return nil, ErrLibraryInvalid
		}
	}

	items := []SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		for _, id := range ids {
			if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, id); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,version,lifecycle_state FROM space_library_items WHERE space_id=$1 AND id=ANY($2) FOR UPDATE`, spaceID, pq.Array(ids))
		if err != nil {
			return err
		}
		found := 0
		states := make(map[string]string, len(ids))
		for rows.Next() {
			var id, state string
			var version int64
			if err := rows.Scan(&id, &version, &state); err != nil {
				_ = rows.Close()
				return err
			}
			if versions[id] != version {
				_ = rows.Close()
				return ErrLibraryConflict
			}
			states[id] = state
			found++
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if found != len(ids) {
			return ErrLibraryNotFound
		}
		requireState := "ready"
		if operation.Action == "restore" {
			requireState = "trash"
		}
		for _, id := range ids {
			if states[id] != requireState {
				return ErrLibraryConflict
			}
		}

		switch operation.Action {
		case "favorite", "unfavorite":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET favorite=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, operation.Action == "favorite", spaceID, pq.Array(ids))
		case "hide", "unhide":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET hidden=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, operation.Action == "hide", spaceID, pq.Array(ids))
		case "trash":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='trash',trashed_at=NOW(),recover_until=NOW()+$1::interval,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, "30 days", spaceID, pq.Array(ids))
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='recovery',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=ANY($2) AND state='active'`, spaceID, pq.Array(ids))
			}
		case "restore":
			result, updateErr := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='ready',trashed_at=NULL,recover_until=NULL,version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2) AND recover_until>NOW()`, spaceID, pq.Array(ids))
			err = updateErr
			if err == nil {
				if count, _ := result.RowsAffected(); count != int64(len(ids)) {
					return ErrLibraryNotFound
				}
				_, err = tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='active',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=ANY($2) AND state='recovery'`, spaceID, pq.Array(ids))
			}
		case "add_to_album", "remove_from_album":
			var albumExists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_albums WHERE id=$1 AND space_id=$2)`, operation.AlbumID, spaceID).Scan(&albumExists); err != nil || !albumExists {
				return ErrLibraryNotFound
			}
			if operation.Action == "add_to_album" {
				_, err = tx.ExecContext(ctx, `INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id) SELECT $1,unnest($2::text[]),$3 ON CONFLICT DO NOTHING`, operation.AlbumID, pq.Array(ids), userID)
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM space_album_items WHERE album_id=$1 AND space_library_item_id=ANY($2)`, operation.AlbumID, pq.Array(ids))
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE space_albums SET version=version+1,updated_at=NOW() WHERE id=$1`, operation.AlbumID)
			}
		case "add_tags":
			raw, _ := json.Marshal(operation.Tags)
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items i SET tags=(SELECT COALESCE(jsonb_agg(value ORDER BY lower(value)),'[]'::jsonb) FROM (SELECT DISTINCT value FROM jsonb_array_elements_text(i.tags||$1::jsonb) value) tags),version=version+1,updated_at=NOW() WHERE i.space_id=$2 AND i.id=ANY($3)`, raw, spaceID, pq.Array(ids))
		case "remove_tags":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items i SET tags=(SELECT COALESCE(jsonb_agg(value ORDER BY lower(value)),'[]'::jsonb) FROM jsonb_array_elements_text(i.tags) value WHERE lower(value)<>ALL($1::text[])),version=version+1,updated_at=NOW() WHERE i.space_id=$2 AND i.id=ANY($3)`, pq.Array(lowerStrings(operation.Tags)), spaceID, pq.Array(ids))
		case "set_date":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET date_override=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, *operation.DateOverride, spaceID, pq.Array(ids))
		case "clear_date":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET date_override=NULL,version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2)`, spaceID, pq.Array(ids))
		case "set_location":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET location_override=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, operation.LocationOverride, spaceID, pq.Array(ids))
		case "clear_location":
			_, err = tx.ExecContext(ctx, `UPDATE space_library_items SET location_override=NULL,version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2)`, spaceID, pq.Array(ids))
		}
		if err != nil {
			return err
		}
		if err := insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.items.bulk."+operation.Action, "library_items", "", "success", map[string]any{"count": len(ids), "album_id": operation.AlbumID}); err != nil {
			return err
		}
		if operation.Action == "trash" || operation.Action == "restore" {
			action := "library.item.trashed"
			if operation.Action == "restore" {
				action = "library.item.restored"
			}
			for _, id := range ids {
				if err := insertLibraryAuditTx(ctx, tx, spaceID, "", userID, action, "library_item", id, "success", map[string]any{"bulk": true}); err != nil {
					return err
				}
			}
		}
		updatedRows, err := tx.QueryContext(ctx, libraryItemSelect+` WHERE i.space_id=$1 AND i.id=ANY($2) ORDER BY array_position($2::text[],i.id)`, spaceID, pq.Array(ids))
		if err != nil {
			return err
		}
		defer updatedRows.Close()
		for updatedRows.Next() {
			var item SpaceLibraryItem
			if err := scanSpaceLibraryItem(updatedRows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return updatedRows.Err()
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
