package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (db *Database) CompleteLibraryUpload(ctx context.Context, userID, spaceID, uploadID, tokenHash string, verifiedSize int64, verifiedSHA, detectedMIME string, intrinsic json.RawMessage) (*CompleteLibraryUploadResult, error) {
	result := &CompleteLibraryUploadResult{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		upload := &result.Upload
		if err := scanLibraryUpload(tx.QueryRowContext(ctx, `SELECT id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,verified_byte_size,COALESCE(verified_sha256,''),COALESCE(detected_mime_type,''),state,COALESCE(file_id,''),upload_token_hash,COALESCE(error_code,''),expires_at,version,created_at,updated_at FROM space_library_uploads WHERE id=$1 AND space_id=$2 AND user_id=$3 FOR UPDATE`, uploadID, spaceID, userID), upload); err != nil {
			return err
		}
		if upload.UploadTokenHash != tokenHash || upload.ExpiresAt.Before(time.Now()) {
			return ErrLibraryForbidden
		}
		if upload.State == "ready" {
			return loadCompletedLibraryUploadTx(ctx, tx, upload, result)
		}
		if upload.State != "uploaded_unverified" && upload.State != "quarantined" && upload.State != "scanning" && upload.State != "processing" {
			return ErrLibraryConflict
		}
		if verifiedSize != upload.RequestedByteSize || verifiedSHA != upload.ClientSHA256 || verifiedSize < 1 || detectedMIME == "" {
			return ErrLibraryUploadMismatch
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:blob:"+upload.SecurityDomainID+":"+verifiedSHA+fmt.Sprint(verifiedSize)); err != nil {
			return err
		}
		blobID, objectKey := "", upload.ObjectKey
		err := tx.QueryRowContext(ctx, `SELECT id,r2_object_key FROM library_blobs WHERE security_domain_id=$1 AND sha256=$2 AND byte_size=$3 AND lifecycle_state='ready' LIMIT 1`, upload.SecurityDomainID, verifiedSHA, verifiedSize).Scan(&blobID, &objectKey)
		if errors.Is(err, sql.ErrNoRows) {
			blobID = "blob_" + uuid.NewString()
			scanStatus := "clean"
			if upload.Purpose == UploadPurposeNoteAttachment ||
				upload.Purpose == UploadPurposeDrawingAsset {
				scanStatus = "skipped"
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'ready','ready')`, blobID, upload.SecurityDomainID, upload.ObjectKey, verifiedSHA, verifiedSize, upload.ClientDeclaredMIMEType, detectedMIME, scanStatus); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if objectKey != upload.ObjectKey {
			result.DiscardObjectKey = upload.ObjectKey
		}
		file := &result.File
		file.ID, file.BlobID, file.SecurityDomainID, file.UploaderUserID, file.OriginalFilename, file.IntrinsicMetadata, file.LifecycleState, file.Version = "file_"+uuid.NewString(), blobID, upload.SecurityDomainID, userID, upload.OriginalFilename, intrinsic, "ready", 1
		if len(file.IntrinsicMetadata) == 0 {
			file.IntrinsicMetadata = json.RawMessage(`{}`)
		}
		var extracted struct {
			CaptureTimestamp string          `json:"capture_timestamp"`
			EmbeddedLocation json.RawMessage `json:"embedded_location"`
		}
		_ = json.Unmarshal(file.IntrinsicMetadata, &extracted)
		var captureAt, intrinsicLocation any
		if parsed, parseErr := time.Parse(time.RFC3339Nano, extracted.CaptureTimestamp); parseErr == nil {
			captureAt = parsed
		}
		if len(extracted.EmbeddedLocation) > 2 && json.Valid(extracted.EmbeddedLocation) {
			intrinsicLocation = extracted.EmbeddedLocation
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO library_files(id,blob_id,security_domain_id,uploader_user_id,original_filename,intrinsic_metadata,intrinsic_capture_at,intrinsic_location,lifecycle_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'ready') RETURNING original_uploaded_at`, file.ID, blobID, upload.SecurityDomainID, userID, upload.OriginalFilename, file.IntrinsicMetadata, captureAt, intrinsicLocation).Scan(&file.OriginalUploadedAt); err != nil {
			return err
		}
		sourceID, sourceKind := "", ""
		if upload.Purpose == "library" {
			item := &SpaceLibraryItem{ID: "item_" + uuid.NewString(), SpaceID: spaceID, FileID: file.ID, ContributingUserID: userID, DisplayName: upload.OriginalFilename, AddedByUserID: userID, LifecycleState: "ready", Version: 1, File: *file}
			item.Tags, item.LocationOverride, item.ContributorInformation = []string{}, json.RawMessage(`null`), json.RawMessage(`{}`)
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_library_items(id,space_id,file_id,contributing_user_id,display_name,added_by_user_id) VALUES($1,$2,$3,$4,$5,$4) RETURNING added_at,updated_at`, item.ID, spaceID, file.ID, userID, upload.OriginalFilename).Scan(&item.AddedAt, &item.UpdatedAt); err != nil {
				return err
			}
			if err := insertDefaultAliasTx(ctx, tx, spaceID, "library_item", item.ID, userID); err != nil {
				return err
			}
			result.Item, sourceID, sourceKind = item, item.ID, "library_item"
		} else if upload.Purpose == UploadPurposeNoteAttachment {
			// A note asset is deliberately not a Library item and not a message
			// attachment: it is reachable only through its parent note's ACL.
			var noteID string
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(note_id,'') FROM space_library_uploads WHERE id=$1`, upload.ID).Scan(&noteID); err != nil {
				return err
			}
			if noteID == "" {
				return ErrLibraryInvalid
			}
			asset := &SpaceNoteAsset{ID: "noteasset_" + uuid.NewString(), NoteID: noteID, FileID: file.ID, UploaderUserID: userID, DisplayName: upload.OriginalFilename, LifecycleState: "ready"}
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_note_assets(id,note_id,file_id,uploader_user_id,display_name) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, asset.ID, noteID, file.ID, userID, upload.OriginalFilename).Scan(&asset.CreatedAt); err != nil {
				return err
			}
			result.NoteAsset, sourceID, sourceKind = asset, asset.ID, "note_asset"
		} else if upload.Purpose == UploadPurposeDrawingAsset {
			// Excalidraw keeps only this stable asset identity in the shared
			// Yjs document. Image bytes stay in R2.
			var drawingID, excalidrawFileID string
			if err := tx.QueryRowContext(
				ctx,
				`SELECT COALESCE(drawing_id,''),COALESCE(drawing_file_id,'')
				 FROM space_library_uploads WHERE id=$1`,
				upload.ID,
			).Scan(&drawingID, &excalidrawFileID); err != nil {
				return err
			}
			if drawingID == "" || excalidrawFileID == "" {
				return ErrLibraryInvalid
			}
			asset := &SpaceDrawingAsset{
				ID: "drawingasset_" + uuid.NewString(), DrawingID: drawingID,
				FileID: file.ID, UploaderUserID: userID,
				ExcalidrawFileID: excalidrawFileID,
				DisplayName:      upload.OriginalFilename,
				LifecycleState:   "ready",
				MIMEType:         detectedMIME,
				ByteSize:         verifiedSize,
				SHA256:           verifiedSHA,
			}
			if err := tx.QueryRowContext(
				ctx,
				`INSERT INTO space_drawing_assets(
				     id,drawing_id,file_id,uploader_user_id,
				     excalidraw_file_id,display_name
				 ) VALUES($1,$2,$3,$4,$5,$6)
				 RETURNING created_at`,
				asset.ID, drawingID, file.ID, userID,
				excalidrawFileID, upload.OriginalFilename,
			).Scan(&asset.CreatedAt); err != nil {
				return err
			}
			result.DrawingAsset, sourceID, sourceKind =
				asset, asset.ID, "drawing_asset"
		} else {
			attachment := &MessageAttachment{ID: "attachment_" + uuid.NewString(), SpaceID: spaceID, FileID: file.ID, UploadID: upload.ID, UploaderUserID: userID, DisplayName: upload.OriginalFilename, LifecycleState: "ready"}
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_message_attachments(id,space_id,file_id,upload_id,uploader_user_id,display_name) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, attachment.ID, spaceID, file.ID, upload.ID, userID, upload.OriginalFilename).Scan(&attachment.CreatedAt); err != nil {
				return err
			}
			if err := insertDefaultAliasTx(ctx, tx, spaceID, "attachment", attachment.ID, userID); err != nil {
				return err
			}
			result.Attachment, sourceID, sourceKind = attachment, attachment.ID, "attachment"
		}
		contributionID := "contribution_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_contributions(id,space_id,user_id,file_id,source_kind,source_id,logical_bytes,state) VALUES($1,$2,$3,$4,$5,$6,$7,'active')`, contributionID, spaceID, userID, file.ID, sourceKind, sourceID, verifiedSize); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_upload_reservations SET state='consumed',updated_at=NOW() WHERE upload_id=$1 AND state='active'`, upload.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),used_bytes=used_bytes+$2,version=version+1,updated_at=NOW() WHERE space_id=$3`, upload.RequestedByteSize, verifiedSize, spaceID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE space_library_uploads SET verified_byte_size=$1,verified_sha256=$2,detected_mime_type=$3,state='ready',file_id=$4,finalized_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$5 RETURNING version,updated_at`, verifiedSize, verifiedSHA, detectedMIME, file.ID, upload.ID).Scan(&upload.Version, &upload.UpdatedAt); err != nil {
			return err
		}
		upload.VerifiedByteSize, upload.VerifiedSHA256, upload.DetectedMIMEType, upload.State, upload.FileID = &verifiedSize, verifiedSHA, detectedMIME, "ready", file.ID
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "library.upload.ready", upload.ID, map[string]any{"upload_id": upload.ID, "state": "ready", "item_id": sourceID, "purpose": upload.Purpose}); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, upload.SecurityDomainID, userID, "library.upload.ready", sourceKind, sourceID, "success", map[string]any{"logical_bytes": verifiedSize, "deduplicated": result.DiscardObjectKey != ""})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return result, err
}

type LibraryItemQuery struct {
	After      string
	Limit      int
	Collection string
	Search     string
	Sort       string
	Direction  string
	MediaType  string
	Utility    string
	Visibility string
	AlbumID    string
	Favorite   bool
	DateFrom   *time.Time
	DateTo     *time.Time
}

type LibraryItemVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type BulkLibraryItemOperation struct {
	Action           string
	Items            []LibraryItemVersion
	AlbumID          string
	Tags             []string
	DateOverride     *time.Time
	LocationOverride json.RawMessage
}

type parsedLibrarySearch struct {
	Text      string
	Tags      []string
	MediaType string
	Album     string
	Favorite  *bool
	Hidden    *bool
	DateFrom  *time.Time
	DateTo    *time.Time
}
