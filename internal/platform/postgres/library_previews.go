package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type LibraryPreviewSource struct {
	FileID           string
	SecurityDomainID string
	SourceIdentity   string
	ObjectKey        string
	MIMEType         string
	ByteSize         int64
	SHA256           string
	PreviewObjectKey string
	PreviewMIME      string
	PreviewBytes     int64
	PreviewSHA256    string
}

type CompleteLibraryPreviewResult struct {
	ObjectKey        string
	MIMEType         string
	ByteSize         int64
	SHA256           string
	DiscardObjectKey string
}

func (db *Database) LibraryItemPreviewSource(ctx context.Context, userID, spaceID, itemID string, original bool) (*LibraryPreviewSource, error) {
	out := &LibraryPreviewSource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		if original {
			if err := tx.QueryRowContext(ctx, `SELECT f.id,f.security_domain_id,b.id,b.r2_object_key,b.server_detected_mime_type,b.byte_size,b.sha256 FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, itemID, spaceID).Scan(&out.FileID, &out.SecurityDomainID, &out.SourceIdentity, &out.ObjectKey, &out.MIMEType, &out.ByteSize, &out.SHA256); err != nil {
				return err
			}
		} else if err := tx.QueryRowContext(ctx, `SELECT f.id,f.security_domain_id,COALESCE(rb.id,b.id),COALESCE(rb.r2_object_key,b.r2_object_key),COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type),COALESCE(rb.byte_size,b.byte_size),COALESCE(rb.sha256,b.sha256)
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready'
			LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'
			WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, itemID, spaceID).Scan(&out.FileID, &out.SecurityDomainID, &out.SourceIdentity, &out.ObjectKey, &out.MIMEType, &out.ByteSize, &out.SHA256); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT pb.r2_object_key,pb.server_detected_mime_type,pb.byte_size,pb.sha256 FROM library_derivatives d JOIN library_blobs pb ON pb.id=d.derivative_blob_id AND pb.lifecycle_state='ready' WHERE d.space_library_item_id=$1 AND d.source_file_id=$2 AND d.kind='image_preview' AND d.lifecycle_state='ready' AND d.metadata->>'source_identity'=$3 ORDER BY d.created_at DESC LIMIT 1`, itemID, out.FileID, out.SourceIdentity).Scan(&out.PreviewObjectKey, &out.PreviewMIME, &out.PreviewBytes, &out.PreviewSHA256)
		if errors.Is(err, sql.ErrNoRows) {
			var ownerID string
			if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1 FOR SHARE`, spaceID).Scan(&ownerID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "owner-storage:"+ownerID); err != nil {
				return err
			}
			var expiredReservation int64
			if releaseErr := tx.QueryRowContext(ctx, `UPDATE space_rendition_reservations SET state='released',updated_at=NOW() WHERE source_kind='preview' AND source_id=$1 AND state='active' AND expires_at<=NOW() RETURNING reserved_bytes`, itemID).Scan(&expiredReservation); releaseErr != nil && !errors.Is(releaseErr, sql.ErrNoRows) {
				return releaseErr
			}
			if expiredReservation > 0 {
				if _, releaseErr := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, expiredReservation, spaceID); releaseErr != nil {
					return releaseErr
				}
			}
			var existingReservation int64
			err := tx.QueryRowContext(ctx, `SELECT reserved_bytes FROM space_rendition_reservations WHERE source_kind='preview' AND source_id=$1 AND state='active' AND expires_at>NOW()`, itemID).Scan(&existingReservation)
			if err == nil {
				// A renderer is already producing this preview. Do not allow a
				// second request to borrow and potentially release its reservation.
				return ErrLibraryConflict
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			ownerUsage, err := ownerStorageUsageTx(ctx, tx, ownerID, true)
			if err != nil {
				return err
			}
			const previewReserve = int64(25_000_000)
			if ownerUsage.RemainingBytes < previewReserve {
				return ErrLibraryQuota
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_rendition_reservations(id,space_id,user_id,source_kind,source_id,reserved_bytes,state,expires_at)
				VALUES($1,$2,$3,'preview',$4,$5,'active',NOW()+INTERVAL '2 hours')
				ON CONFLICT(source_kind,source_id) DO UPDATE SET id=EXCLUDED.id,space_id=EXCLUDED.space_id,user_id=EXCLUDED.user_id,reserved_bytes=EXCLUDED.reserved_bytes,state='active',expires_at=EXCLUDED.expires_at,updated_at=NOW()`, "preview_reservation_"+uuid.NewString(), spaceID, userID, itemID, previewReserve); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`, previewReserve, spaceID)
			return err
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

// ReleaseLibraryPreviewReservation returns capacity when preview generation
// fails before completion. It is safe to call after a successful completion.
func (db *Database) ReleaseLibraryPreviewReservation(ctx context.Context, userID, spaceID, itemID string) error {
	if itemID == "" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var released int64
		err := tx.QueryRowContext(ctx, `UPDATE space_rendition_reservations SET state='released',updated_at=NOW() WHERE space_id=$1 AND source_kind='preview' AND source_id=$2 AND user_id=$3 AND state='active' RETURNING reserved_bytes`, spaceID, itemID, userID).Scan(&released)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID)
		return err
	})
}

func (db *Database) CompleteLibraryPreview(ctx context.Context, userID, spaceID, itemID, sourceIdentity, objectKey, mimeType string, byteSize int64, sha string, original bool) (*CompleteLibraryPreviewResult, error) {
	if itemID == "" || sourceIdentity == "" || objectKey == "" || mimeType != "image/jpeg" || byteSize < 1 || byteSize > 25_000_000 || len(sha) != 64 {
		return nil, ErrLibraryInvalid
	}
	out := &CompleteLibraryPreviewResult{ObjectKey: objectKey, MIMEType: mimeType, ByteSize: byteSize, SHA256: sha}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		var fileID, domainID, currentIdentity string
		if original {
			if err := tx.QueryRowContext(ctx, `SELECT f.id,f.security_domain_id,b.id FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready'`, itemID, spaceID).Scan(&fileID, &domainID, &currentIdentity); err != nil {
				return err
			}
		} else if err := tx.QueryRowContext(ctx, `SELECT f.id,f.security_domain_id,COALESCE(rb.id,b.id) FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready' LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready' WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready'`, itemID, spaceID).Scan(&fileID, &domainID, &currentIdentity); err != nil {
			return err
		}
		if currentIdentity != sourceIdentity {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:preview:"+spaceID+":"+itemID+":"+sourceIdentity); err != nil {
			return err
		}
		var existing CompleteLibraryPreviewResult
		err := tx.QueryRowContext(ctx, `SELECT pb.r2_object_key,pb.server_detected_mime_type,pb.byte_size,pb.sha256 FROM library_derivatives d JOIN library_blobs pb ON pb.id=d.derivative_blob_id AND pb.lifecycle_state='ready' WHERE d.space_library_item_id=$1 AND d.kind='image_preview' AND d.lifecycle_state='ready' AND d.metadata->>'source_identity'=$2 LIMIT 1`, itemID, sourceIdentity).Scan(&existing.ObjectKey, &existing.MIMEType, &existing.ByteSize, &existing.SHA256)
		if err == nil {
			var released int64
			if releaseErr := tx.QueryRowContext(ctx, `UPDATE space_rendition_reservations SET state='released',updated_at=NOW() WHERE source_kind='preview' AND source_id=$1 AND state='active' RETURNING reserved_bytes`, itemID).Scan(&released); releaseErr != nil && !errors.Is(releaseErr, sql.ErrNoRows) {
				return releaseErr
			}
			if released > 0 {
				if _, releaseErr := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); releaseErr != nil {
					return releaseErr
				}
			}
			out.ObjectKey, out.MIMEType, out.ByteSize, out.SHA256, out.DiscardObjectKey = existing.ObjectKey, existing.MIMEType, existing.ByteSize, existing.SHA256, objectKey
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var reserved int64
		if err := tx.QueryRowContext(ctx, `SELECT reserved_bytes FROM space_rendition_reservations WHERE source_kind='preview' AND source_id=$1 AND state='active' AND expires_at>NOW() FOR UPDATE`, itemID).Scan(&reserved); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryConflict
		} else if err != nil {
			return err
		}
		if byteSize > reserved {
			return ErrLibraryQuota
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:blob:"+domainID+":"+sha+fmt.Sprint(byteSize)); err != nil {
			return err
		}
		blobID, existingKey := "", ""
		err = tx.QueryRowContext(ctx, `SELECT id,r2_object_key FROM library_blobs WHERE security_domain_id=$1 AND sha256=$2 AND byte_size=$3 AND lifecycle_state='ready' LIMIT 1`, domainID, sha, byteSize).Scan(&blobID, &existingKey)
		if errors.Is(err, sql.ErrNoRows) {
			blobID = "blob_" + uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state) VALUES($1,$2,$3,$4,$5,$6,$6,'clean','ready','ready')`, blobID, domainID, objectKey, sha, byteSize, mimeType); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if existingKey != objectKey {
			out.ObjectKey, out.DiscardObjectKey = existingKey, objectKey
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_derivatives(id,security_domain_id,source_file_id,space_library_item_id,derivative_blob_id,kind,metadata,lifecycle_state) VALUES($1,$2,$3,$4,$5,'image_preview',jsonb_build_object('source_identity',$6::text),'ready')`, "derivative_"+uuid.NewString(), domainID, fileID, itemID, blobID, sourceIdentity); err != nil {
			return err
		}
		if result, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET logical_bytes=logical_bytes+$1,updated_at=NOW() WHERE space_id=$2 AND source_kind='library_item' AND source_id=$3 AND state='active'`, byteSize, spaceID, itemID); err != nil {
			return err
		} else if count, _ := result.RowsAffected(); count != 1 {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_rendition_reservations SET state='consumed',updated_at=NOW() WHERE source_kind='preview' AND source_id=$1 AND state='active'`, itemID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=used_bytes+$1,reserved_bytes=GREATEST(0,reserved_bytes-$2),version=version+1,updated_at=NOW() WHERE space_id=$3`, byteSize, reserved, spaceID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, domainID, userID, "library.preview.generated", "library_item", itemID, "success", map[string]any{"byte_size": byteSize})
	})
	return out, err
}

// ReplaceMissingLibraryPreviewDeduplicationObject heals a preview blob whose
// database row is still ready even though its immutable object disappeared.
// The caller must verify that missingKey is absent and replacementKey contains
// the freshly rendered preview with the same metadata.
func (db *Database) ReplaceMissingLibraryPreviewDeduplicationObject(ctx context.Context, userID, spaceID, itemID, sourceIdentity, missingKey, replacementKey string) (string, error) {
	if itemID == "" || sourceIdentity == "" || missingKey == "" || replacementKey == "" || missingKey == replacementKey {
		return "", ErrLibraryInvalid
	}
	selectedKey := ""
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var blobID, domainID, sha, currentKey string
		var byteSize int64
		if err := tx.QueryRowContext(ctx, `SELECT pb.id,pb.security_domain_id,pb.sha256,pb.byte_size,pb.r2_object_key
			FROM library_derivatives d
			JOIN space_library_items i ON i.id=d.space_library_item_id AND i.space_id=$2 AND i.lifecycle_state='ready'
			JOIN library_blobs pb ON pb.id=d.derivative_blob_id AND pb.lifecycle_state='ready'
			WHERE d.space_library_item_id=$1 AND d.kind='image_preview' AND d.lifecycle_state='ready' AND d.metadata->>'source_identity'=$3
			ORDER BY d.created_at DESC LIMIT 1`, itemID, spaceID, sourceIdentity).
			Scan(&blobID, &domainID, &sha, &byteSize, &currentKey); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:blob:"+domainID+":"+sha+fmt.Sprint(byteSize)); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT r2_object_key FROM library_blobs WHERE id=$1 AND lifecycle_state='ready' FOR UPDATE`, blobID).Scan(&currentKey); err != nil {
			return err
		}
		if currentKey != missingKey {
			selectedKey = currentKey
			return nil
		}
		result, err := tx.ExecContext(ctx, `UPDATE library_blobs SET r2_object_key=$1,version=version+1,updated_at=NOW() WHERE id=$2 AND r2_object_key=$3`, replacementKey, blobID, missingKey)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrLibraryConflict
		}
		selectedKey = replacementKey
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryNotFound
	}
	return selectedKey, err
}
