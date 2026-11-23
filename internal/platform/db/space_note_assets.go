package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type SpaceNoteAsset struct {
	ID             string    `json:"id"`
	NoteID         string    `json:"note_id"`
	FileID         string    `json:"file_id"`
	UploaderUserID string    `json:"uploader_user_id"`
	DisplayName    string    `json:"display_name"`
	LifecycleState string    `json:"lifecycle_state"`
	CreatedAt      time.Time `json:"created_at"`
	MIMEType       string    `json:"mime_type"`
	ByteSize       int64     `json:"byte_size"`
	SHA256         string    `json:"sha256"`
}

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
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT n.space_id,s.security_domain_id
			 FROM space_notes n JOIN spaces s ON s.id=n.space_id WHERE n.id=$1 FOR SHARE OF s`,
			noteID).Scan(&out.SpaceID, &out.SecurityDomainID); err != nil {
			return err
		}
		if err := prepareStorageReservationTx(ctx, tx, userID, out.SpaceID); err != nil {
			return err
		}
		if err := requireSpaceUploadSizeTx(ctx, tx, userID, out.SpaceID, byteSize); err != nil {
			return err
		}
		if err := RequireStorageRoomTx(ctx, tx, userID, out.SpaceID, byteSize); err != nil {
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

func (db *Database) NoteAssets(ctx context.Context, userID, noteID string) ([]SpaceNoteAsset, error) {
	assets := []SpaceNoteAsset{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT a.id,a.note_id,a.file_id,a.uploader_user_id,a.display_name,a.lifecycle_state,a.created_at,
			        COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256
			 FROM space_note_assets a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.note_id=$1 AND a.lifecycle_state='ready' ORDER BY a.created_at`, noteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset SpaceNoteAsset
			if err := rows.Scan(&asset.ID, &asset.NoteID, &asset.FileID, &asset.UploaderUserID,
				&asset.DisplayName, &asset.LifecycleState, &asset.CreatedAt, &asset.MIMEType, &asset.ByteSize, &asset.SHA256); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}

func (db *Database) NoteAssetDownload(ctx context.Context, userID, noteID, assetID string) (*LibraryDownload, error) {
	download := &LibraryDownload{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
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

func (db *Database) DeleteNoteAsset(ctx context.Context, userID, noteID, assetID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
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
			return nil
		}
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_notes WHERE id=$1`, noteID).Scan(&spaceID); err != nil {
			return err
		}
		return recordNoteEventTx(ctx, tx, spaceID, userID, "note.projection.updated", noteID, nil)
	})
}
