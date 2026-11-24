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

func (db *Database) MessageAttachmentDownload(ctx context.Context, userID, spaceID, attachmentID string) (*LibraryDownload, error) {
	return db.messageAttachmentDownload(ctx, userID, spaceID, attachmentID, false)
}

func (db *Database) DirectAttachmentDownload(ctx context.Context, userID, dmID, attachmentID string) (*LibraryDownload, error) {
	return db.messageAttachmentDownload(ctx, userID, dmID, attachmentID, true)
}

func (db *Database) messageAttachmentDownload(ctx context.Context, userID, spaceID, attachmentID string, direct bool) (*LibraryDownload, error) {
	out := &LibraryDownload{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if direct {
			if err := requireInDirectTx(ctx, tx, userID, spaceID); err != nil {
				return err
			}
		} else if isDirect, err := isDirectSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		} else if isDirect {
			return ErrLibraryNotFound
		} else if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return ErrLibraryForbidden
		}
		return tx.QueryRowContext(ctx, `SELECT b.r2_object_key,a.display_name,b.server_detected_mime_type,b.byte_size,b.sha256
			FROM space_message_attachments a
			JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			LEFT JOIN space_messages m ON m.id=a.message_id
			WHERE a.id=$1 AND a.space_id=$2 AND a.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'
			  AND ((a.message_id IS NULL AND a.uploader_user_id=$3) OR `+spaceMessageVisibleTo("$3")+`)`, attachmentID, spaceID, userID).
			Scan(&out.ObjectKey, &out.Filename, &out.MIMEType, &out.ByteSize, &out.SHA256)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

func loadCompletedLibraryUploadTx(ctx context.Context, tx *sql.Tx, upload *LibraryUpload, result *CompleteLibraryUploadResult) error {
	if err := scanLibraryFile(tx.QueryRowContext(ctx, `SELECT id,blob_id,security_domain_id,uploader_user_id,original_filename,intrinsic_metadata,lifecycle_state,original_uploaded_at,version FROM library_files WHERE id=$1`, upload.FileID), &result.File); err != nil {
		return err
	}
	if upload.Purpose == UploadPurposeNoteAttachment {
		asset := &SpaceNoteAsset{}
		if err := tx.QueryRowContext(
			ctx,
			`SELECT a.id,a.note_id,a.file_id,a.uploader_user_id,a.display_name,
			        a.lifecycle_state,a.created_at,
			        COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256
			 FROM space_note_assets a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.file_id=$1`,
			upload.FileID,
		).Scan(
			&asset.ID, &asset.NoteID, &asset.FileID,
			&asset.UploaderUserID, &asset.DisplayName,
			&asset.LifecycleState, &asset.CreatedAt,
			&asset.MIMEType, &asset.ByteSize, &asset.SHA256,
		); err != nil {
			return err
		}
		result.NoteAsset = asset
	} else {
		attachment := &MessageAttachment{}
		if err := scanMessageAttachment(tx.QueryRowContext(ctx, `SELECT id,space_id,COALESCE(message_id,''),file_id,upload_id,uploader_user_id,display_name,lifecycle_state,created_at,deleted_at,recover_until FROM space_message_attachments WHERE upload_id=$1`, upload.ID), attachment); err != nil {
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
	_, err = tx.ExecContext(ctx, `INSERT INTO space_library_audit_events(request_id,security_domain_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, "req_"+hex.EncodeToString(requestHash[:8]), securityDomainID, spaceID, actorUserID, action, targetKind, targetID, outcome, raw)
	return err
}

func scanLibraryUpload(scanner interface{ Scan(...any) error }, out *LibraryUpload) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.SecurityDomainID, &out.UserID, &out.ObjectKey, &out.OriginalFilename, &out.Purpose, &out.ClientDeclaredMIMEType, &out.RequestedByteSize, &out.ClientSHA256, &out.VerifiedByteSize, &out.VerifiedSHA256, &out.DetectedMIMEType, &out.State, &out.FileID, &out.UploadTokenHash, &out.ErrorCode, &out.ExpiresAt, &out.Version, &out.CreatedAt, &out.UpdatedAt)
}

func scanLibraryFile(scanner interface{ Scan(...any) error }, out *LibraryFile) error {
	return scanner.Scan(&out.ID, &out.BlobID, &out.SecurityDomainID, &out.UploaderUserID, &out.OriginalFilename, &out.IntrinsicMetadata, &out.LifecycleState, &out.OriginalUploadedAt, &out.Version)
}

func scanMessageAttachment(scanner interface{ Scan(...any) error }, out *MessageAttachment) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.MessageID, &out.FileID, &out.UploadID, &out.UploaderUserID, &out.DisplayName, &out.LifecycleState, &out.CreatedAt, &out.DeletedAt, &out.RecoverUntil)
}
