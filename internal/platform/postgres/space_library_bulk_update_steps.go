package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/lib/pq"
)

// validateBulkLibraryItemRefs checks the requested item count, rejects blank
// ids, non-positive versions and duplicates, and returns each item's expected
// version along with the ids in request order.
func validateBulkLibraryItemRefs(refs []LibraryItemVersion) (map[string]int64, []string, error) {
	if len(refs) < 1 || len(refs) > 200 {
		return nil, nil, ErrLibraryInvalid
	}
	versions := make(map[string]int64, len(refs))
	ids := make([]string, 0, len(refs))
	for _, item := range refs {
		if strings.TrimSpace(item.ID) == "" || item.Version < 1 {
			return nil, nil, ErrLibraryInvalid
		}
		if _, duplicate := versions[item.ID]; duplicate {
			return nil, nil, ErrLibraryInvalid
		}
		versions[item.ID] = item.Version
		ids = append(ids, item.ID)
	}
	return versions, ids, nil
}

// validateBulkLibraryOperation checks the action and the fields it needs, and
// normalizes tags for the tag actions.
func validateBulkLibraryOperation(operation *BulkLibraryItemOperation) error {
	allowedAction := map[string]bool{
		"favorite": true, "unfavorite": true, "hide": true, "unhide": true,
		"trash": true, "restore": true, "add_to_album": true, "remove_from_album": true,
		"add_tags": true, "remove_tags": true, "set_date": true, "clear_date": true,
		"set_location": true, "clear_location": true,
	}
	if !allowedAction[operation.Action] || (operation.Action == "add_to_album" || operation.Action == "remove_from_album") && operation.AlbumID == "" {
		return ErrLibraryInvalid
	}
	if operation.Action == "add_tags" || operation.Action == "remove_tags" {
		operation.Tags = normalizeLibraryTags(operation.Tags)
		if len(operation.Tags) < 1 || len(operation.Tags) > 100 {
			return ErrLibraryInvalid
		}
	}
	if operation.Action == "set_date" && operation.DateOverride == nil {
		return ErrLibraryInvalid
	}
	if operation.Action == "set_location" {
		var location map[string]any
		if len(operation.LocationOverride) < 2 || len(operation.LocationOverride) > 4096 || json.Unmarshal(operation.LocationOverride, &location) != nil || len(location) == 0 {
			return ErrLibraryInvalid
		}
	}
	return nil
}

// authorizeBulkLibraryItemsTx requires library edit permission in the space and
// audience access to every requested item.
func authorizeBulkLibraryItemsTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, ids []string) error {
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
		return err
	}
	for _, id := range ids {
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, id); err != nil {
			return err
		}
	}
	return nil
}

// lockBulkLibraryItemStatesTx row-locks the requested items, rejects any whose
// version no longer matches, and returns each item's lifecycle state.
func lockBulkLibraryItemStatesTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string, versions map[string]int64) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,version,lifecycle_state FROM space_library_items WHERE space_id=$1 AND id=ANY($2) FOR UPDATE`, spaceID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	found := 0
	states := make(map[string]string, len(ids))
	for rows.Next() {
		var id, state string
		var version int64
		if err := rows.Scan(&id, &version, &state); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if versions[id] != version {
			_ = rows.Close()
			return nil, ErrLibraryConflict
		}
		states[id] = state
		found++
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if found != len(ids) {
		return nil, ErrLibraryNotFound
	}
	return states, nil
}

// requireBulkLibraryItemStates rejects the batch unless every item is in the
// lifecycle state the action needs: trashed for restore, ready otherwise.
func requireBulkLibraryItemStates(action string, ids []string, states map[string]string) error {
	requireState := "ready"
	if action == "restore" {
		requireState = "trash"
	}
	for _, id := range ids {
		if states[id] != requireState {
			return ErrLibraryConflict
		}
	}
	return nil
}

// applyBulkLibraryItemMutationTx runs the SQL for the requested action against
// the locked items.
func applyBulkLibraryItemMutationTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, operation BulkLibraryItemOperation, ids []string) error {
	switch operation.Action {
	case "favorite", "unfavorite":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items SET favorite=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, operation.Action == "favorite", spaceID, pq.Array(ids))
		return err
	case "hide", "unhide":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items SET hidden=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, operation.Action == "hide", spaceID, pq.Array(ids))
		return err
	case "trash":
		return trashBulkLibraryItemsTx(ctx, tx, spaceID, ids)
	case "restore":
		return restoreBulkLibraryItemsTx(ctx, tx, spaceID, ids)
	case "add_to_album", "remove_from_album":
		return applyBulkAlbumMembershipTx(ctx, tx, userID, spaceID, operation, ids)
	case "add_tags":
		raw, _ := json.Marshal(operation.Tags)
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items i SET tags=(SELECT COALESCE(jsonb_agg(value ORDER BY lower(value)),'[]'::jsonb) FROM (SELECT DISTINCT value FROM jsonb_array_elements_text(i.tags||$1::jsonb) value) tags),version=version+1,updated_at=NOW() WHERE i.space_id=$2 AND i.id=ANY($3)`, raw, spaceID, pq.Array(ids))
		return err
	case "remove_tags":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items i SET tags=(SELECT COALESCE(jsonb_agg(value ORDER BY lower(value)),'[]'::jsonb) FROM jsonb_array_elements_text(i.tags) value WHERE lower(value)<>ALL($1::text[])),version=version+1,updated_at=NOW() WHERE i.space_id=$2 AND i.id=ANY($3)`, pq.Array(lowerStrings(operation.Tags)), spaceID, pq.Array(ids))
		return err
	case "set_date":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items SET date_override=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, *operation.DateOverride, spaceID, pq.Array(ids))
		return err
	case "clear_date":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items SET date_override=NULL,version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2)`, spaceID, pq.Array(ids))
		return err
	case "set_location":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items SET location_override=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, operation.LocationOverride, spaceID, pq.Array(ids))
		return err
	case "clear_location":
		_, err := tx.ExecContext(ctx, `UPDATE space_library_items SET location_override=NULL,version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2)`, spaceID, pq.Array(ids))
		return err
	}
	return nil
}

// trashBulkLibraryItemsTx moves the items to the trash and parks their storage
// contributions in recovery.
func trashBulkLibraryItemsTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='trash',trashed_at=NOW(),recover_until=NOW()+$1::interval,version=version+1,updated_at=NOW() WHERE space_id=$2 AND id=ANY($3)`, "30 days", spaceID, pq.Array(ids)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='recovery',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=ANY($2) AND state='active'`, spaceID, pq.Array(ids))
	return err
}

// restoreBulkLibraryItemsTx brings trashed items back while they are still
// within their recovery window, and reactivates their storage contributions.
func restoreBulkLibraryItemsTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string) error {
	result, err := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='ready',trashed_at=NULL,recover_until=NULL,version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2) AND recover_until>NOW()`, spaceID, pq.Array(ids))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != int64(len(ids)) {
		return ErrLibraryNotFound
	}
	_, err = tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='active',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=ANY($2) AND state='recovery'`, spaceID, pq.Array(ids))
	return err
}

// applyBulkAlbumMembershipTx adds the items to, or removes them from, the
// requested album and bumps the album version.
func applyBulkAlbumMembershipTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, operation BulkLibraryItemOperation, ids []string) error {
	var albumExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_albums WHERE id=$1 AND space_id=$2)`, operation.AlbumID, spaceID).Scan(&albumExists); err != nil || !albumExists {
		return ErrLibraryNotFound
	}
	var err error
	if operation.Action == "add_to_album" {
		_, err = tx.ExecContext(ctx, `INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id) SELECT $1,unnest($2::text[]),$3 ON CONFLICT DO NOTHING`, operation.AlbumID, pq.Array(ids), userID)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM space_album_items WHERE album_id=$1 AND space_library_item_id=ANY($2)`, operation.AlbumID, pq.Array(ids))
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE space_albums SET version=version+1,updated_at=NOW() WHERE id=$1`, operation.AlbumID)
	}
	return err
}

// recordBulkLibraryAuditTx writes the bulk audit entry and, for trash and
// restore, one entry per item.
func recordBulkLibraryAuditTx(ctx context.Context, tx *sql.Tx, spaceID, userID string, operation BulkLibraryItemOperation, ids []string) error {
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
	return nil
}

// loadBulkLibraryItemsTx appends the updated items to items in request order.
func loadBulkLibraryItemsTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string, items *[]SpaceLibraryItem) error {
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
		*items = append(*items, item)
	}
	return updatedRows.Err()
}
