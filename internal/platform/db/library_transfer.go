package db

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lib/pq"
)

type LibraryTransferItem struct {
	ItemID              string
	SecurityDomainID    string
	ObjectKey           string
	Filename            string
	MIMEType            string
	ByteSize            int64
	SHA256              string
	IntrinsicMetadata   json.RawMessage
	Rendition           bool
	RenditionDefinition json.RawMessage
}

func (db *Database) LibraryTransferItems(ctx context.Context, userID, spaceID string, itemIDs []string) ([]LibraryTransferItem, error) {
	itemIDs = uniqueSpaceIDs(itemIDs)
	if len(itemIDs) < 1 || len(itemIDs) > 200 {
		return nil, ErrLibraryInvalid
	}
	items := []LibraryTransferItem{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryDownload); err != nil {
			return err
		}
		for _, itemID := range itemIDs {
			if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,f.security_domain_id,COALESCE(rb.r2_object_key,b.r2_object_key),i.display_name,COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type),COALESCE(rb.byte_size,b.byte_size),COALESCE(rb.sha256,b.sha256),f.intrinsic_metadata,(rb.id IS NOT NULL),COALESCE(v.edit_definition,'{}'::jsonb)
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready'
			LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'
			WHERE i.space_id=$1 AND i.id=ANY($2) AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			ORDER BY array_position($2::text[],i.id)`, spaceID, pq.Array(itemIDs))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibraryTransferItem
			if err := rows.Scan(&item.ItemID, &item.SecurityDomainID, &item.ObjectKey, &item.Filename, &item.MIMEType, &item.ByteSize, &item.SHA256, &item.IntrinsicMetadata, &item.Rendition, &item.RenditionDefinition); err != nil {
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(items) != len(itemIDs) {
			return ErrLibraryNotFound
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.transfer.authorized", "library_items", "", "success", map[string]any{"count": len(items)})
	})
	return items, err
}
