package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SpaceDrawingAsset is an R2-backed Excalidraw binary file. The shared scene
// stores only ID references; image bytes never enter PostgreSQL or the
// collaboration Durable Object.
type SpaceDrawingAsset struct {
	ID               string    `json:"id"`
	DrawingID        string    `json:"drawing_id"`
	FileID           string    `json:"file_id"`
	UploaderUserID   string    `json:"uploader_user_id"`
	ExcalidrawFileID string    `json:"excalidraw_file_id"`
	DisplayName      string    `json:"display_name"`
	LifecycleState   string    `json:"lifecycle_state"`
	CreatedAt        time.Time `json:"created_at"`
	MIMEType         string    `json:"mime_type"`
	ByteSize         int64     `json:"byte_size"`
	SHA256           string    `json:"sha256"`
}

func scanSpaceDrawingAsset(scanner interface{ Scan(...any) error }, asset *SpaceDrawingAsset) error {
	return scanner.Scan(
		&asset.ID,
		&asset.DrawingID,
		&asset.FileID,
		&asset.UploaderUserID,
		&asset.ExcalidrawFileID,
		&asset.DisplayName,
		&asset.LifecycleState,
		&asset.CreatedAt,
		&asset.MIMEType,
		&asset.ByteSize,
		&asset.SHA256,
	)
}

// CreateDrawingAssetUpload reserves owner-pool quota after rechecking current
// edit access to the parent drawing.
func (db *Database) CreateDrawingAssetUpload(
	ctx context.Context,
	userID, drawingID, excalidrawFileID, filename, declaredMIME string,
	byteSize int64,
	clientSHA, objectKey, tokenHash string,
	expiresAt time.Time,
) (*LibraryUpload, error) {
	maxBytes := MaxUploadBytesForPurpose(UploadPurposeDrawingAsset)
	if byteSize < 1 || byteSize > maxBytes || len(clientSHA) != 64 ||
		excalidrawFileID == "" || len(excalidrawFileID) > 160 ||
		filename == "" || objectKey == "" || tokenHash == "" {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryUpload{
		ID: "upload_" + uuid.NewString(), UserID: userID,
		ObjectKey: objectKey, OriginalFilename: filename,
		Purpose:                UploadPurposeDrawingAsset,
		ClientDeclaredMIMEType: declaredMIME,
		RequestedByteSize:      byteSize,
		ClientSHA256:           clientSHA,
		State:                  "initiated",
		UploadTokenHash:        tokenHash,
		ExpiresAt:              expiresAt,
		Version:                1,
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
		var ownerID string
		if err := tx.QueryRowContext(
			ctx,
			`SELECT d.space_id,s.security_domain_id,s.owner_user_id
			 FROM space_drawings d
			 JOIN spaces s ON s.id=d.space_id
			 WHERE d.id=$1 AND d.lifecycle_state='active'
			 FOR SHARE OF s`,
			drawingID,
		).Scan(&out.SpaceID, &out.SecurityDomainID, &ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`,
			"owner-storage:"+ownerID,
		); err != nil {
			return err
		}
		usage, err := ownerStorageUsageTx(ctx, tx, ownerID, true)
		if err != nil {
			return err
		}
		if usage.UsedBytes+usage.ReservedBytes+byteSize > usage.LimitBytes {
			return ErrLibraryQuota
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO space_storage_usage(space_id)
			 VALUES($1) ON CONFLICT DO NOTHING`,
			out.SpaceID,
		); err != nil {
			return err
		}
		if err := tx.QueryRowContext(
			ctx,
			`INSERT INTO space_library_uploads(
			     id,space_id,security_domain_id,user_id,object_key,
			     original_filename,purpose,client_declared_mime_type,
			     requested_byte_size,client_sha256,state,upload_token_hash,
			     expires_at,drawing_id,drawing_file_id
			 ) VALUES(
			     $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'initiated',
			     $11,$12,$13,$14
			 ) RETURNING created_at,updated_at`,
			out.ID, out.SpaceID, out.SecurityDomainID, userID,
			objectKey, filename, out.Purpose, declaredMIME,
			byteSize, clientSHA, tokenHash, expiresAt,
			drawingID, excalidrawFileID,
		).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO space_upload_reservations(
			     upload_id,space_id,user_id,reserved_bytes,state,expires_at
			 ) VALUES($1,$2,$3,$4,'active',$5)`,
			out.ID, out.SpaceID, userID, byteSize, expiresAt,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE space_storage_usage
			 SET reserved_bytes=reserved_bytes+$1,
			     version=version+1,updated_at=NOW()
			 WHERE space_id=$2`,
			byteSize, out.SpaceID,
		); err != nil {
			return err
		}
		return insertLibraryAuditTx(
			ctx, tx, out.SpaceID, out.SecurityDomainID, userID,
			"drawing.asset.upload.initiated", "upload", out.ID, "success",
			map[string]any{
				"drawing_id":         drawingID,
				"excalidraw_file_id": excalidrawFileID,
				"reserved_bytes":     byteSize,
			},
		)
	})
	return out, err
}

// DrawingAssets lists ready file references for a current drawing member.
func (db *Database) DrawingAssets(
	ctx context.Context,
	userID, drawingID string,
) ([]SpaceDrawingAsset, error) {
	assets := []SpaceDrawingAsset{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrLibraryNotFound
		}
		rows, err := tx.QueryContext(
			ctx,
			`SELECT a.id,a.drawing_id,a.file_id,a.uploader_user_id,
			        a.excalidraw_file_id,a.display_name,a.lifecycle_state,
			        a.created_at,
			        COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			        b.byte_size,b.sha256
			 FROM space_drawing_assets a
			 JOIN library_files f ON f.id=a.file_id
			 JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.drawing_id=$1 AND a.lifecycle_state='ready'
			 ORDER BY a.created_at`,
			drawingID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset SpaceDrawingAsset
			if err := scanSpaceDrawingAsset(rows, &asset); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}

// DrawingAssetDownload resolves an R2 object only after checking the drawing.
func (db *Database) DrawingAssetDownload(
	ctx context.Context,
	userID, drawingID, assetID string,
) (*LibraryDownload, error) {
	download := &LibraryDownload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrLibraryNotFound
		}
		return tx.QueryRowContext(
			ctx,
			`SELECT b.r2_object_key,a.display_name,
			        COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			        b.byte_size,b.sha256
			 FROM space_drawing_assets a
			 JOIN library_files f ON f.id=a.file_id
			 JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.id=$1 AND a.drawing_id=$2
			   AND a.lifecycle_state='ready'`,
			assetID, drawingID,
		).Scan(
			&download.ObjectKey, &download.Filename, &download.MIMEType,
			&download.ByteSize, &download.SHA256,
		)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	if err != nil {
		return nil, err
	}
	return download, nil
}

// DeleteDrawingAsset marks a reference unreferenced. R2 cleanup happens only
// after the collaboration/undo safety window; this endpoint never deletes a
// shared image synchronously.
func (db *Database) DeleteDrawingAsset(
	ctx context.Context,
	userID, drawingID, assetID string,
) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
		_, err = tx.ExecContext(
			ctx,
			`UPDATE space_drawing_assets
			 SET lifecycle_state='unreferenced',deleted_at=NOW()
			 WHERE id=$1 AND drawing_id=$2 AND lifecycle_state='ready'`,
			assetID, drawingID,
		)
		return err
	})
}
