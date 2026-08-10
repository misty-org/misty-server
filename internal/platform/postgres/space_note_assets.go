package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SpaceNoteAsset is a verified file attached to one note. Assets inherit access
// exclusively from their parent note and never appear in the Space Library.
type SpaceNoteAsset struct {
	ID             string    `json:"id"`
	NoteID         string    `json:"note_id"`
	FileID         string    `json:"file_id"`
	UploaderUserID string    `json:"uploader_user_id"`
	DisplayName    string    `json:"display_name"`
	LifecycleState string    `json:"lifecycle_state"`
	CreatedAt      time.Time `json:"created_at"`
}

// CreateNoteAssetUpload reserves quota and creates a pending note_attachment
// upload.
//
// This exists separately from CreateLibraryUpload because the authorization
// question is different: not "may this user upload to this Space" but "may this
// user edit this note". A Space member with library.upload but no note grant
// must be rejected here.
func (db *Database) CreateNoteAssetUpload(ctx context.Context, userID, noteID, filename, declaredMIME string, byteSize int64, clientSHA, objectKey, tokenHash string, expiresAt time.Time) (*LibraryUpload, error) {
	maxBytes := MaxUploadBytesForPurpose(UploadPurposeNoteAttachment)
	if byteSize < 1 || byteSize > maxBytes || len(clientSHA) != 64 || filename == "" || objectKey == "" || tokenHash == "" {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryUpload{
		ID: "upload_" + uuid.NewString(), UserID: userID, ObjectKey: objectKey,
		OriginalFilename: filename, Purpose: UploadPurposeNoteAttachment,
		ClientDeclaredMIMEType: declaredMIME, RequestedByteSize: byteSize, ClientSHA256: clientSHA,
		State: "initiated", UploadTokenHash: tokenHash, ExpiresAt: expiresAt, Version: 1,
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			// Viewers, ungranted members, and Space owners all land here with
			// the same answer as a note that does not exist.
			return ErrLibraryNotFound
		}
		var ownerID string
		if err := tx.QueryRowContext(ctx,
			`SELECT n.space_id,s.security_domain_id,s.owner_user_id
			 FROM space_notes n JOIN spaces s ON s.id=n.space_id WHERE n.id=$1 FOR SHARE OF s`,
			noteID).Scan(&out.SpaceID, &out.SecurityDomainID, &ownerID); err != nil {
			return err
		}
		// Note assets consume the same owner storage pool as Library files, so
		// they take the same advisory lock to keep quota accounting serialized.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "owner-storage:"+ownerID); err != nil {
			return err
		}
		ownerUsage, err := ownerStorageUsageTx(ctx, tx, ownerID, true)
		if err != nil {
			return err
		}
		if ownerUsage.UsedBytes+ownerUsage.ReservedBytes+byteSize > ownerUsage.LimitBytes {
			return ErrLibraryQuota
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, out.SpaceID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at,note_id)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'initiated',$11,$12,$13) RETURNING created_at,updated_at`,
			out.ID, out.SpaceID, out.SecurityDomainID, userID, objectKey, filename, out.Purpose,
			declaredMIME, byteSize, clientSHA, tokenHash, expiresAt, noteID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,$4,'active',$5)`,
			out.ID, out.SpaceID, userID, byteSize, expiresAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`,
			byteSize, out.SpaceID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, out.SpaceID, out.SecurityDomainID, userID,
			"note.asset.upload.initiated", "upload", out.ID, "success",
			map[string]any{"note_id": noteID, "reserved_bytes": byteSize})
	})
	return out, err
}

// NoteAssets lists a note's ready assets for a caller who may view it.
func (db *Database) NoteAssets(ctx context.Context, userID, noteID string) ([]SpaceNoteAsset, error) {
	assets := []SpaceNoteAsset{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT id,note_id,file_id,uploader_user_id,display_name,lifecycle_state,created_at
			 FROM space_note_assets WHERE note_id=$1 AND lifecycle_state='ready' ORDER BY created_at`, noteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset SpaceNoteAsset
			if err := rows.Scan(&asset.ID, &asset.NoteID, &asset.FileID, &asset.UploaderUserID,
				&asset.DisplayName, &asset.LifecycleState, &asset.CreatedAt); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}

// NoteAssetDownload resolves the object behind an asset for a caller who may
// view the parent note. Viewers may download; only editing is restricted.
func (db *Database) NoteAssetDownload(ctx context.Context, userID, noteID, assetID string) (*LibraryDownload, error) {
	download := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrLibraryNotFound
		}
		return tx.QueryRowContext(ctx,
			`SELECT b.r2_object_key,a.display_name,COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256
			 FROM space_note_assets a
			 JOIN library_files f ON f.id=a.file_id
			 JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.id=$1 AND a.note_id=$2 AND a.lifecycle_state='ready'`,
			assetID, noteID).Scan(&download.ObjectKey, &download.Filename, &download.MIMEType,
			&download.ByteSize, &download.SHA256)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	if err != nil {
		return nil, err
	}
	return download, nil
}

// DeleteNoteAsset removes an asset.
//
// Only the creator may permanently remove an asset. An editor removing the
// reference from the document marks it unreferenced instead, so the retention
// window still applies and a concurrent collaborator's view does not break.
func (db *Database) DeleteNoteAsset(ctx context.Context, userID, noteID, assetID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
		nextState := "unreferenced"
		if access.CanDelete {
			nextState = "deleting"
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE space_note_assets SET lifecycle_state=$1,deleted_at=NOW()
			 WHERE id=$2 AND note_id=$3 AND lifecycle_state='ready'`,
			nextState, assetID, noteID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			// Already removed, or never existed. Either way the caller's intent
			// is satisfied, so a retry does not surface as an error.
			return nil
		}
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_notes WHERE id=$1`, noteID).Scan(&spaceID); err != nil {
			return err
		}
		return recordNoteEventTx(ctx, tx, spaceID, userID, "note.projection.updated", noteID, nil)
	})
}
