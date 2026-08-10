package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) LibraryItemDownload(ctx context.Context, userID, spaceID, itemID string) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryDownload); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(rb.r2_object_key,b.r2_object_key),i.display_name,COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type),COALESCE(rb.byte_size,b.byte_size),COALESCE(rb.sha256,b.sha256),(rb.id IS NOT NULL)
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready'
			LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'
			WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, itemID, spaceID).
			Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256, &out.Rendition); err != nil {
			return err
		}
		return recordLibraryItemViewTx(ctx, tx, userID, spaceID, itemID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func (db *Database) LibraryOriginalItemDownload(ctx context.Context, userID, spaceID, itemID string) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryDownload); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT b.r2_object_key,f.original_filename,b.server_detected_mime_type,b.byte_size,b.sha256
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, itemID, spaceID).
			Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256); err != nil {
			return err
		}
		return recordLibraryItemViewTx(ctx, tx, userID, spaceID, itemID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func recordLibraryItemViewTx(ctx context.Context, tx *sql.Tx, userID, spaceID, itemID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO space_library_item_views(space_id,space_library_item_id,user_id) VALUES($1,$2,$3)
		ON CONFLICT(space_id,space_library_item_id,user_id) DO UPDATE SET view_count=space_library_item_views.view_count+1,last_viewed_at=NOW()`, spaceID, itemID, userID)
	return err
}

func (db *Database) MessageAttachmentDownload(ctx context.Context, userID, spaceID, attachmentID string) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return ErrLibraryForbidden
		}
		return tx.QueryRowContext(ctx, `SELECT b.r2_object_key,a.display_name,b.server_detected_mime_type,b.byte_size,b.sha256
			FROM space_message_attachments a
			JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN space_messages m ON m.id=a.message_id
			WHERE a.id=$1 AND a.space_id=$2 AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			  AND ((a.message_id IS NULL AND a.uploader_user_id=$3) OR m.conversation_id IS NULL OR EXISTS(
				SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=m.conversation_id AND cm.user_id=$3
			  ))`, attachmentID, spaceID, userID).
			Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

// ExplicitAgentLibraryItemDownload resolves a Library item selected by a human
// for Agent context. It requires current Library visibility but does not grant
// the Agent general download or enumeration access.
func (db *Database) ExplicitAgentLibraryItemDownload(ctx context.Context, userID, spaceID, itemID string) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return ErrLibraryForbidden
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT COALESCE(rb.r2_object_key,b.r2_object_key),i.display_name,
			COALESCE(rb.server_detected_mime_type,b.server_detected_mime_type),COALESCE(rb.byte_size,b.byte_size),
			COALESCE(rb.sha256,b.sha256),(rb.id IS NOT NULL)
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN library_item_versions v ON v.id=i.current_edit_version_id AND v.lifecycle_state='ready' AND v.rendition_state='ready'
			LEFT JOIN library_blobs rb ON rb.id=v.rendition_blob_id AND rb.lifecycle_state='ready'
			WHERE i.id=$1 AND i.space_id=$2 AND i.hidden=FALSE AND i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, itemID, spaceID).
			Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256, &out.Rendition)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func (db *Database) ExplicitAgentTaskAttachmentDownload(ctx context.Context, userID, spaceID, attachmentID string) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return ErrLibraryForbidden
		}
		return tx.QueryRowContext(ctx, `SELECT b.r2_object_key,a.display_name,b.server_detected_mime_type,b.byte_size,b.sha256
			FROM space_message_attachments a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			WHERE a.id=$1 AND a.space_id=$2 AND a.message_id IS NULL AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, attachmentID, spaceID).
			Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

const libraryItemSelect = `SELECT i.id,i.space_id,i.file_id,i.contributing_user_id,i.display_name,i.caption,i.tags,i.favorite,i.hidden,i.date_override,COALESCE(i.location_override,'null'::jsonb),i.contributor_information,COALESCE(i.current_edit_version_id,''),i.added_by_user_id,i.lifecycle_state,i.added_at,i.trashed_at,i.recover_until,i.version,i.updated_at,
	f.id,f.blob_id,f.security_domain_id,f.uploader_user_id,f.original_filename,f.intrinsic_metadata,f.lifecycle_state,f.original_uploaded_at,f.version FROM space_library_items i JOIN library_files f ON f.id=i.file_id`

func loadCompletedLibraryUploadTx(ctx context.Context, tx *sql.Tx, upload *LibraryUpload, result *CompleteLibraryUploadResult) error {
	if err := scanLibraryFile(tx.QueryRowContext(ctx, `SELECT id,blob_id,security_domain_id,uploader_user_id,original_filename,intrinsic_metadata,lifecycle_state,original_uploaded_at,version FROM library_files WHERE id=$1`, upload.FileID), &result.File); err != nil {
		return err
	}
	if upload.Purpose == "library" {
		item := &SpaceLibraryItem{}
		if err := scanSpaceLibraryItem(tx.QueryRowContext(ctx, libraryItemSelect+` WHERE i.file_id=$1 AND i.space_id=$2 AND i.added_by_user_id=$3 ORDER BY i.added_at DESC LIMIT 1`, upload.FileID, upload.SpaceID, upload.UserID), item); err != nil {
			return err
		}
		result.Item = item
	} else if upload.Purpose == UploadPurposeNoteAttachment {
		asset := &SpaceNoteAsset{}
		if err := tx.QueryRowContext(
			ctx,
			`SELECT id,note_id,file_id,uploader_user_id,display_name,
			        lifecycle_state,created_at
			 FROM space_note_assets
			 WHERE file_id=$1`,
			upload.FileID,
		).Scan(
			&asset.ID, &asset.NoteID, &asset.FileID,
			&asset.UploaderUserID, &asset.DisplayName,
			&asset.LifecycleState, &asset.CreatedAt,
		); err != nil {
			return err
		}
		result.NoteAsset = asset
	} else if upload.Purpose == UploadPurposeDrawingAsset {
		asset := &SpaceDrawingAsset{}
		if err := scanSpaceDrawingAsset(tx.QueryRowContext(
			ctx,
			`SELECT a.id,a.drawing_id,a.file_id,a.uploader_user_id,
			        a.excalidraw_file_id,a.display_name,a.lifecycle_state,
			        a.created_at,
			        COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			        b.byte_size,b.sha256
			 FROM space_drawing_assets a
			 JOIN library_files f ON f.id=a.file_id
			 JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.file_id=$1`,
			upload.FileID,
		), asset); err != nil {
			return err
		}
		result.DrawingAsset = asset
	} else {
		attachment := &MessageAttachment{}
		if err := scanMessageAttachment(tx.QueryRowContext(ctx, `SELECT id,space_id,COALESCE(message_id,''),file_id,upload_id,uploader_user_id,display_name,COALESCE(promoted_item_id,''),lifecycle_state,created_at,deleted_at,recover_until FROM space_message_attachments WHERE upload_id=$1`, upload.ID), attachment); err != nil {
			return err
		}
		result.Attachment = attachment
	}
	return nil
}

func insertDefaultAliasTx(ctx context.Context, tx *sql.Tx, spaceID, kind, targetID, userID string) error {
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	alias := strings.TrimSuffix(kind, "_item") + "_" + raw
	_, err := tx.ExecContext(ctx, `INSERT INTO space_item_aliases(id,space_id,target_kind,target_id,alias,normalized_alias,created_by_user_id) VALUES($1,$2,$3,$4,$5,$5,$6)`, "alias_"+uuid.NewString(), spaceID, kind, targetID, alias, userID)
	return err
}

func insertLibraryAuditTx(ctx context.Context, tx *sql.Tx, spaceID, domainID, userID, action, targetKind, targetID, outcome string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	requestHash := sha256.Sum256([]byte(uuid.NewString()))
	var securityDomainID any
	if domainID != "" {
		securityDomainID = domainID
	}
	var actorUserID any
	if userID != "" {
		actorUserID = userID
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO space_library_audit_events(request_id,security_domain_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, "req_"+hex.EncodeToString(requestHash[:8]), securityDomainID, spaceID, actorUserID, action, targetKind, targetID, outcome, raw); err != nil {
		return err
	}
	if outcome != "success" || !libraryAuditRequiresRealtime(action) {
		return nil
	}
	_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, action, targetID, map[string]any{
		"action":      action,
		"target_kind": targetKind,
		"target_id":   targetID,
		"outcome":     outcome,
	})
	return err
}

func libraryAuditRequiresRealtime(action string) bool {
	for _, prefix := range []string{
		"library.item.",
		"library.items.",
		"library.album.",
		"library.album_folder.",
		"library.asset_stack.",
		"library.edit.",
		"library.people.",
		"library.intelligence.",
		"library.memory.",
		"library.duplicates.",
		"library.pins.",
		"library.import.",
		"library.grant.",
	} {
		if strings.HasPrefix(action, prefix) {
			return true
		}
	}
	return false
}

func normalizeLibraryTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, value := range tags {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || len([]rune(value)) > 80 || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func lowerStrings(values []string) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = strings.ToLower(value)
	}
	return out
}

func scanLibraryUpload(scanner interface{ Scan(...any) error }, out *LibraryUpload) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.SecurityDomainID, &out.UserID, &out.ObjectKey, &out.OriginalFilename, &out.Purpose, &out.ClientDeclaredMIMEType, &out.RequestedByteSize, &out.ClientSHA256, &out.VerifiedByteSize, &out.VerifiedSHA256, &out.DetectedMIMEType, &out.State, &out.FileID, &out.UploadTokenHash, &out.ErrorCode, &out.ExpiresAt, &out.Version, &out.CreatedAt, &out.UpdatedAt)
}

func scanLibraryFile(scanner interface{ Scan(...any) error }, out *LibraryFile) error {
	return scanner.Scan(&out.ID, &out.BlobID, &out.SecurityDomainID, &out.UploaderUserID, &out.OriginalFilename, &out.IntrinsicMetadata, &out.LifecycleState, &out.OriginalUploadedAt, &out.Version)
}

func scanSpaceLibraryItem(scanner interface{ Scan(...any) error }, out *SpaceLibraryItem) error {
	var tags []byte
	err := scanner.Scan(&out.ID, &out.SpaceID, &out.FileID, &out.ContributingUserID, &out.DisplayName, &out.Caption, &tags, &out.Favorite, &out.Hidden, &out.DateOverride, &out.LocationOverride, &out.ContributorInformation, &out.CurrentEditVersionID, &out.AddedByUserID, &out.LifecycleState, &out.AddedAt, &out.TrashedAt, &out.RecoverUntil, &out.Version, &out.UpdatedAt,
		&out.File.ID, &out.File.BlobID, &out.File.SecurityDomainID, &out.File.UploaderUserID, &out.File.OriginalFilename, &out.File.IntrinsicMetadata, &out.File.LifecycleState, &out.File.OriginalUploadedAt, &out.File.Version)
	if err == nil {
		_ = json.Unmarshal(tags, &out.Tags)
	}
	return err
}

func scanMessageAttachment(scanner interface{ Scan(...any) error }, out *MessageAttachment) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.MessageID, &out.FileID, &out.UploadID, &out.UploaderUserID, &out.DisplayName, &out.PromotedItemID, &out.LifecycleState, &out.CreatedAt, &out.DeletedAt, &out.RecoverUntil)
}
