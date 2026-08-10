package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ReplaceMissingLibraryUploadDeduplicationObject heals a deduplicated blob
// whose R2 object disappeared. The caller must first verify that missingKey is
// absent and that the upload's new object exists with the reserved metadata.
func (db *Database) ReplaceMissingLibraryUploadDeduplicationObject(ctx context.Context, userID, spaceID, uploadID, missingKey string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryUpload); err != nil {
			return err
		}
		var domainID, sha, uploadKey, state string
		var byteSize int64
		if err := tx.QueryRowContext(ctx, `SELECT security_domain_id,client_sha256,requested_byte_size,object_key,state
			FROM space_library_uploads WHERE id=$1 AND space_id=$2 AND user_id=$3 FOR UPDATE`, uploadID, spaceID, userID).
			Scan(&domainID, &sha, &byteSize, &uploadKey, &state); err != nil {
			return err
		}
		if state != "uploaded_unverified" || missingKey == "" || uploadKey == missingKey {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:blob:"+domainID+":"+sha+fmt.Sprint(byteSize)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE library_blobs
			SET r2_object_key=$1,version=version+1,updated_at=NOW()
			WHERE security_domain_id=$2 AND sha256=$3 AND byte_size=$4 AND lifecycle_state='ready' AND r2_object_key=$5`,
			uploadKey, domainID, sha, byteSize, missingKey)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 1 {
			return nil
		}
		var currentKey string
		if err := tx.QueryRowContext(ctx, `SELECT r2_object_key FROM library_blobs WHERE security_domain_id=$1 AND sha256=$2 AND byte_size=$3 AND lifecycle_state='ready' LIMIT 1`, domainID, sha, byteSize).Scan(&currentKey); err != nil {
			return err
		}
		if currentKey == uploadKey {
			return nil
		}
		return ErrLibraryConflict
	})
}

func (db *Database) SetLibraryUploadState(ctx context.Context, userID, spaceID, uploadID, tokenHash, from, to string) (*LibraryUpload, error) {
	out := &LibraryUpload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanLibraryUpload(tx.QueryRowContext(ctx, `UPDATE space_library_uploads SET state=$1,version=version+1,updated_at=NOW()
			WHERE id=$2 AND space_id=$3 AND user_id=$4 AND upload_token_hash=$5 AND state=$6 AND expires_at>NOW()
			RETURNING id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,verified_byte_size,COALESCE(verified_sha256,''),COALESCE(detected_mime_type,''),state,COALESCE(file_id,''),upload_token_hash,COALESCE(error_code,''),expires_at,version,created_at,updated_at`, to, uploadID, spaceID, userID, tokenHash, from), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryConflict
	}
	return out, err
}

func (db *Database) RejectLibraryUpload(ctx context.Context, userID, spaceID, uploadID, tokenHash, state, errorCode string) error {
	if state != "rejected" && state != "infected" && state != "invalid" && state != "processing_failed" && state != "expired" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var upload LibraryUpload
		if err := scanLibraryUpload(tx.QueryRowContext(ctx, `SELECT id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,verified_byte_size,COALESCE(verified_sha256,''),COALESCE(detected_mime_type,''),state,COALESCE(file_id,''),upload_token_hash,COALESCE(error_code,''),expires_at,version,created_at,updated_at FROM space_library_uploads WHERE id=$1 AND space_id=$2 AND user_id=$3 FOR UPDATE`, uploadID, spaceID, userID), &upload); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryNotFound
			}
			return err
		}
		if upload.UploadTokenHash != tokenHash {
			return ErrLibraryForbidden
		}
		if upload.State == "ready" {
			return ErrLibraryConflict
		}
		var released int64
		if err := tx.QueryRowContext(ctx, `UPDATE space_upload_reservations SET state='released',updated_at=NOW() WHERE upload_id=$1 AND state='active' RETURNING reserved_bytes`, upload.ID).Scan(&released); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if released > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state=$1,error_code=$2,version=version+1,updated_at=NOW() WHERE id=$3`, state, errorCode, upload.ID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "library.upload."+state, upload.ID, map[string]any{"upload_id": upload.ID, "state": state, "error_code": errorCode}); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, upload.SecurityDomainID, userID, "library.upload."+state, "upload", upload.ID, "failed", map[string]any{"error_code": errorCode, "released_bytes": released})
	})
}

func (db *Database) ExpireLibraryUploads(ctx context.Context, limit int) ([]ExpiredLibraryUpload, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	out := []ExpiredLibraryUpload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.object_key,u.space_id,u.security_domain_id,u.user_id,r.reserved_bytes
			FROM space_library_uploads u JOIN space_upload_reservations r ON r.upload_id=u.id
			WHERE r.state='active' AND u.expires_at<=NOW() AND u.state NOT IN ('ready','deleted','expired')
			ORDER BY u.expires_at FOR UPDATE OF u,r SKIP LOCKED LIMIT $1`, limit)
		if err != nil {
			return err
		}
		type candidate struct {
			id, key, spaceID, domainID, userID string
			reserved                           int64
		}
		candidates := []candidate{}
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.key, &item.spaceID, &item.domainID, &item.userID, &item.reserved); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range candidates {
			if _, err := tx.ExecContext(ctx, `UPDATE space_upload_reservations SET state='released',updated_at=NOW() WHERE upload_id=$1 AND state='active'`, item.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, item.reserved, item.spaceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='expired',error_code='upload_expired',version=version+1,updated_at=NOW() WHERE id=$1`, item.id); err != nil {
				return err
			}
			if err := insertLibraryAuditTx(ctx, tx, item.spaceID, item.domainID, item.userID, "library.upload.expired", "upload", item.id, "failed", map[string]any{"released_bytes": item.reserved}); err != nil {
				return err
			}
			out = append(out, ExpiredLibraryUpload{ID: item.id, ObjectKey: item.key})
		}
		return nil
	})
	return out, err
}

func (db *Database) ReconcileLibraryStorageUsage(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	updated := 0
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `WITH candidates AS (
			SELECT space_id FROM space_storage_usage ORDER BY updated_at LIMIT $1 FOR UPDATE SKIP LOCKED
		), actual AS (
			SELECT c.space_id,
				COALESCE((SELECT sum(logical_bytes) FROM space_storage_contributions sc WHERE sc.space_id=c.space_id AND sc.state IN ('active','recovery')),0) used,
				COALESCE((SELECT sum(reserved_bytes) FROM space_upload_reservations sr WHERE sr.space_id=c.space_id AND sr.state='active'),0)
				+ COALESCE((SELECT sum(reserved_bytes) FROM space_rendition_reservations rr WHERE rr.space_id=c.space_id AND rr.state='active'),0) reserved
			FROM candidates c
		)
		UPDATE space_storage_usage u SET used_bytes=a.used,reserved_bytes=a.reserved,version=u.version+1,updated_at=NOW()
		FROM actual a WHERE u.space_id=a.space_id AND (u.used_bytes<>a.used OR u.reserved_bytes<>a.reserved)`, limit)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		updated = int(count)
		return nil
	})
	return updated, err
}

func (db *Database) TrashLibraryItem(ctx context.Context, userID, spaceID, itemID string) (*SpaceLibraryItem, error) {
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='trash',trashed_at=NOW(),recover_until=NOW()+$1::interval,version=version+1,updated_at=NOW() WHERE id=$2 AND space_id=$3 AND lifecycle_state='ready'`, "30 days", itemID, spaceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryNotFound
		}
		if _, err = tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='recovery',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=$2 AND state='active'`, spaceID, itemID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.item.trashed", "library_item", itemID, "success", map[string]any{})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryItem(ctx, userID, spaceID, itemID)
}

func (db *Database) RestoreLibraryItem(ctx context.Context, userID, spaceID, itemID string) (*SpaceLibraryItem, error) {
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='ready',trashed_at=NULL,recover_until=NULL,version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND lifecycle_state='trash' AND recover_until>NOW()`, itemID, spaceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryNotFound
		}
		if _, err = tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='active',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=$2 AND state='recovery'`, spaceID, itemID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.item.restored", "library_item", itemID, "success", map[string]any{})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryItem(ctx, userID, spaceID, itemID)
}
