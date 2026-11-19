package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SpaceDeckAsset is an R2-backed image a deck's slides show. A slide's image slot keeps only the
// asset's id ({type:"image", asset_id}); the bytes never enter PostgreSQL or the deck's room.
// Assets follow their deck's access exactly, as a drawing's do.
type SpaceDeckAsset struct {
	ID             string    `json:"id"`
	DeckID         string    `json:"deck_id"`
	FileID         string    `json:"file_id"`
	UploaderUserID string    `json:"uploader_user_id"`
	DisplayName    string    `json:"display_name"`
	LifecycleState string    `json:"lifecycle_state"`
	CreatedAt      time.Time `json:"created_at"`
	MIMEType       string    `json:"mime_type"`
	ByteSize       int64     `json:"byte_size"`
	SHA256         string    `json:"sha256"`
}

const deckAssetColumns = `a.id,a.deck_id,a.file_id,a.uploader_user_id,a.display_name,a.lifecycle_state,a.created_at,
	COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),b.byte_size,b.sha256`

func scanSpaceDeckAsset(scanner interface{ Scan(...any) error }, asset *SpaceDeckAsset) error {
	return scanner.Scan(&asset.ID, &asset.DeckID, &asset.FileID, &asset.UploaderUserID, &asset.DisplayName,
		&asset.LifecycleState, &asset.CreatedAt, &asset.MIMEType, &asset.ByteSize, &asset.SHA256)
}

func deckKind() SpaceItemKind {
	kind, _ := SpaceItemKindNamed(SpaceItemDeck)
	return kind
}

// CreateDeckAssetUpload reserves personal and Space quota after rechecking edit access to the
// deck. Same limits and flow as a drawing's images.
func (db *Database) CreateDeckAssetUpload(
	ctx context.Context,
	userID, deckID, filename, declaredMIME string,
	byteSize int64,
	clientSHA, objectKey, tokenHash string,
	expiresAt time.Time,
) (*LibraryUpload, error) {
	maxBytes := MaxUploadBytesForPurpose(UploadPurposeDeckAsset)
	if byteSize < 1 || byteSize > maxBytes || len(clientSHA) != 64 ||
		filename == "" || objectKey == "" || tokenHash == "" {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryUpload{
		ID: "upload_" + uuid.NewString(), UserID: userID, ObjectKey: objectKey,
		OriginalFilename: filename, Purpose: UploadPurposeDeckAsset,
		ClientDeclaredMIMEType: declaredMIME, RequestedByteSize: byteSize, ClientSHA256: clientSHA,
		State: "initiated", UploadTokenHash: tokenHash, ExpiresAt: expiresAt, Version: 1,
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, deckKind(), userID, deckID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT d.space_id,s.security_domain_id FROM space_decks d JOIN spaces s ON s.id=d.space_id
			 WHERE d.id=$1 AND d.lifecycle_state='active' FOR SHARE OF s`, deckID,
		).Scan(&out.SpaceID, &out.SecurityDomainID); err != nil {
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
			`INSERT INTO space_library_uploads(
			     id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,
			     client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,
			     expires_at,deck_id
			 ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'initiated',$11,$12,$13)
			 RETURNING created_at,updated_at`,
			out.ID, out.SpaceID, out.SecurityDomainID, userID, objectKey, filename, out.Purpose,
			declaredMIME, byteSize, clientSHA, tokenHash, expiresAt, deckID,
		).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at)
			 VALUES($1,$2,$3,$4,'active',$5)`, out.ID, out.SpaceID, userID, byteSize, expiresAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW()
			 WHERE space_id=$2`, byteSize, out.SpaceID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, out.SpaceID, out.SecurityDomainID, userID,
			"deck.asset.upload.initiated", "upload", out.ID, "success",
			map[string]any{"deck_id": deckID, "reserved_bytes": byteSize})
	})
	return out, err
}

// DeckAssets lists a deck's ready images for a caller who can view it.
func (db *Database) DeckAssets(ctx context.Context, userID, deckID string) ([]SpaceDeckAsset, error) {
	assets := []SpaceDeckAsset{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, deckKind(), userID, deckID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrLibraryNotFound
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT `+deckAssetColumns+`
			 FROM space_deck_assets a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.deck_id=$1 AND a.lifecycle_state='ready' ORDER BY a.created_at`, deckID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset SpaceDeckAsset
			if err := scanSpaceDeckAsset(rows, &asset); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}

// DeckAssetDownload resolves an image's object only after checking the deck.
func (db *Database) DeckAssetDownload(ctx context.Context, userID, deckID, assetID string) (*LibraryDownload, error) {
	download := &LibraryDownload{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, deckKind(), userID, deckID)
		if err != nil {
			return err
		}
		if !access.CanView {
			return ErrLibraryNotFound
		}
		return tx.QueryRowContext(ctx,
			`SELECT b.r2_object_key,a.display_name,COALESCE(b.server_detected_mime_type,b.client_declared_mime_type),
			        b.byte_size,b.sha256
			 FROM space_deck_assets a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
			 WHERE a.id=$1 AND a.deck_id=$2 AND a.lifecycle_state='ready'`, assetID, deckID,
		).Scan(&download.ObjectKey, &download.Filename, &download.MIMEType, &download.ByteSize, &download.SHA256)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	if err != nil {
		return nil, err
	}
	return download, nil
}

// DeleteDeckAsset marks an image unreferenced. Its bytes go only after the collaboration and
// undo safety window, so a concurrent editor's slide doesn't break.
func (db *Database) DeleteDeckAsset(ctx context.Context, userID, deckID, assetID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := madeItemAccessForTx(ctx, tx, deckKind(), userID, deckID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE space_deck_assets SET lifecycle_state='unreferenced',deleted_at=NOW()
			 WHERE id=$1 AND deck_id=$2 AND lifecycle_state='ready'`, assetID, deckID)
		return err
	})
}

// insertDeckAssetTx makes a finalized deck upload into the deck's asset.
func insertDeckAssetTx(ctx context.Context, tx *sql.Tx, upload *LibraryUpload, file *LibraryFile, userID, detectedMIME, verifiedSHA string, verifiedSize int64) (*SpaceDeckAsset, error) {
	var deckID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(deck_id,'') FROM space_library_uploads WHERE id=$1`, upload.ID).Scan(&deckID); err != nil {
		return nil, err
	}
	if deckID == "" {
		return nil, ErrLibraryInvalid
	}
	asset := &SpaceDeckAsset{
		ID: "deckasset_" + uuid.NewString(), DeckID: deckID, FileID: file.ID, UploaderUserID: userID,
		DisplayName: upload.OriginalFilename, LifecycleState: "ready",
		MIMEType: detectedMIME, ByteSize: verifiedSize, SHA256: verifiedSHA,
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO space_deck_assets(id,deck_id,file_id,uploader_user_id,display_name)
		 VALUES($1,$2,$3,$4,$5) RETURNING created_at`,
		asset.ID, deckID, file.ID, userID, upload.OriginalFilename).Scan(&asset.CreatedAt); err != nil {
		return nil, err
	}
	return asset, nil
}

// loadDeckAssetForFileTx is a finished deck upload's asset, for a repeated finalize.
func loadDeckAssetForFileTx(ctx context.Context, tx *sql.Tx, fileID string) (*SpaceDeckAsset, error) {
	asset := &SpaceDeckAsset{}
	err := scanSpaceDeckAsset(tx.QueryRowContext(ctx,
		`SELECT `+deckAssetColumns+`
		 FROM space_deck_assets a JOIN library_files f ON f.id=a.file_id JOIN library_blobs b ON b.id=f.blob_id
		 WHERE a.file_id=$1`, fileID), asset)
	return asset, err
}
