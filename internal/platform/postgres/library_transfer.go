package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
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

type LibraryImportRecord struct {
	ID                 string    `json:"id"`
	SourceSpaceID      string    `json:"source_space_id"`
	SourceItemID       string    `json:"source_item_id"`
	DestinationSpaceID string    `json:"destination_space_id"`
	DestinationItemID  string    `json:"destination_item_id"`
	LogicalBytes       int64     `json:"logical_bytes"`
	State              string    `json:"state"`
	CreatedAt          time.Time `json:"created_at"`
}

type LibraryImportHistoryItem struct {
	ID                   string     `json:"id"`
	Direction            string     `json:"direction"`
	SourceSpaceID        string     `json:"source_space_id"`
	DestinationSpaceID   string     `json:"destination_space_id"`
	CounterpartSpaceName string     `json:"counterpart_space_name"`
	ItemID               string     `json:"item_id"`
	DisplayName          string     `json:"display_name"`
	LogicalBytes         int64      `json:"logical_bytes"`
	State                string     `json:"state"`
	CreatedAt            time.Time  `json:"created_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

func (db *Database) LibraryTransferItems(ctx context.Context, userID, spaceID string, itemIDs []string) ([]LibraryTransferItem, error) {
	itemIDs = uniqueSpaceIDs(itemIDs)
	if len(itemIDs) < 1 || len(itemIDs) > 200 {
		return nil, ErrLibraryInvalid
	}
	items := []LibraryTransferItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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

func (db *Database) RecordLibraryImport(ctx context.Context, userID, sourceSpaceID, sourceItemID, destinationSpaceID, destinationItemID, uploadID string, logicalBytes int64) (*LibraryImportRecord, error) {
	if sourceSpaceID == destinationSpaceID || sourceItemID == "" || destinationItemID == "" || uploadID == "" || logicalBytes < 1 {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryImportRecord{ID: "import_" + uuid.NewString(), SourceSpaceID: sourceSpaceID, SourceItemID: sourceItemID, DestinationSpaceID: destinationSpaceID, DestinationItemID: destinationItemID, LogicalBytes: logicalBytes, State: "ready"}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, sourceSpaceID, PermissionLibraryDownload); err != nil {
			return err
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, destinationSpaceID, PermissionLibraryImport); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, sourceSpaceID, sourceItemID); err != nil {
			return err
		}
		var sourceDomainID, destinationDomainID string
		if err := tx.QueryRowContext(ctx, `SELECT f.security_domain_id FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND i.audience_kind='space'`, sourceItemID, sourceSpaceID).Scan(&sourceDomainID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryNotFound
			}
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT f.security_domain_id FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready'`, destinationItemID, destinationSpaceID).Scan(&destinationDomainID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryNotFound
			}
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_library_imports(id,source_space_id,source_item_id,source_security_domain_id,destination_space_id,destination_item_id,destination_security_domain_id,importer_user_id,quota_reservation_upload_id,logical_bytes,state,completed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ready',NOW()) RETURNING created_at`, out.ID, sourceSpaceID, sourceItemID, sourceDomainID, destinationSpaceID, destinationItemID, destinationDomainID, userID, uploadID, logicalBytes).Scan(&out.CreatedAt); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, destinationSpaceID, destinationDomainID, userID, "library.import.completed", "import", out.ID, "success", map[string]any{"logical_bytes": logicalBytes, "source_space_id": sourceSpaceID})
	})
	return out, err
}

func (db *Database) LibraryImportHistory(ctx context.Context, userID, spaceID string, limit int) ([]LibraryImportHistoryItem, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	out := []LibraryImportHistoryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT h.id,CASE WHEN h.destination_space_id=$1 THEN 'incoming' ELSE 'outgoing' END,h.source_space_id,h.destination_space_id,
			CASE WHEN EXISTS(SELECT 1 FROM space_members member WHERE member.space_id=CASE WHEN h.destination_space_id=$1 THEN h.source_space_id ELSE h.destination_space_id END AND member.user_id=$2)
				THEN CASE WHEN h.destination_space_id=$1 THEN source_space.name ELSE destination_space.name END ELSE 'Unavailable space' END,
			CASE WHEN h.destination_space_id=$1 THEN CASE WHEN destination_item.lifecycle_state='ready' AND destination_item.hidden=FALSE THEN COALESCE(h.destination_item_id,'') ELSE '' END ELSE CASE WHEN source_item.lifecycle_state='ready' AND source_item.hidden=FALSE THEN h.source_item_id ELSE '' END END,
			CASE WHEN h.destination_space_id=$1 THEN CASE WHEN destination_item.lifecycle_state='ready' AND destination_item.hidden=FALSE THEN COALESCE(destination_item.display_name,'Imported item') ELSE 'Protected item' END ELSE CASE WHEN source_item.lifecycle_state='ready' AND source_item.hidden=FALSE THEN COALESCE(source_item.display_name,'Imported item') ELSE 'Protected item' END END,
			h.logical_bytes,h.state,h.created_at,h.completed_at
			FROM space_library_imports h JOIN spaces source_space ON source_space.id=h.source_space_id JOIN spaces destination_space ON destination_space.id=h.destination_space_id
			LEFT JOIN space_library_items source_item ON source_item.id=h.source_item_id LEFT JOIN space_library_items destination_item ON destination_item.id=h.destination_item_id
			WHERE h.source_space_id=$1 OR h.destination_space_id=$1 ORDER BY h.created_at DESC LIMIT $3`, spaceID, userID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibraryImportHistoryItem
			if err := rows.Scan(&item.ID, &item.Direction, &item.SourceSpaceID, &item.DestinationSpaceID, &item.CounterpartSpaceName, &item.ItemID, &item.DisplayName, &item.LogicalBytes, &item.State, &item.CreatedAt, &item.CompletedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
