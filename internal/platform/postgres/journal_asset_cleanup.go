package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	JournalAssetKindNote    = "note_asset"
	JournalAssetKindDrawing = "drawing_asset"
)

// JournalAssetPurge is a claimed note/drawing asset cleanup. ObjectKey is
// present only when this asset owns the last live file reference to the blob.
// Shared, deduplicated blobs are deliberately retained.
type JournalAssetPurge struct {
	Kind       string
	AssetID    string
	FileID     string
	BlobID     string
	ObjectKey  string
	DeleteBlob bool
}

// ClaimExpiredJournalAssets moves old, unreferenced Journal assets into a
// retryable deleting state. It releases logical quota and only claims an R2
// object when no other live file or direct blob reference exists.
func (db *Database) ClaimExpiredJournalAssets(
	ctx context.Context,
	safetyWindow time.Duration,
	limit int,
) ([]JournalAssetPurge, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if safetyWindow < time.Hour {
		safetyWindow = 24 * time.Hour
	}

	claims := make([]JournalAssetPurge, 0, limit)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		for _, source := range []struct {
			kind, table string
		}{
			{JournalAssetKindNote, "space_note_assets"},
			{JournalAssetKindDrawing, "space_drawing_assets"},
		} {
			remaining := limit - len(claims)
			if remaining == 0 {
				break
			}
			query := fmt.Sprintf(
				`SELECT a.id,a.file_id
				 FROM %s a
				 WHERE a.lifecycle_state IN ('unreferenced','deleting')
				   AND a.deleted_at IS NOT NULL
				   AND a.deleted_at<=NOW()-$1::interval
				   AND NOT EXISTS (
				       SELECT 1 FROM library_legal_holds h
				       WHERE h.active AND h.target_kind=$2 AND h.target_id=a.id
				   )
				 ORDER BY a.deleted_at
				 FOR UPDATE SKIP LOCKED LIMIT $3`,
				source.table,
			)
			rows, err := tx.QueryContext(
				ctx, query, safetyWindow.String(), source.kind, remaining,
			)
			if err != nil {
				return err
			}
			type candidate struct{ assetID, fileID string }
			candidates := make([]candidate, 0, remaining)
			for rows.Next() {
				var item candidate
				if err := rows.Scan(&item.assetID, &item.fileID); err != nil {
					rows.Close()
					return err
				}
				candidates = append(candidates, item)
			}
			if err := rows.Close(); err != nil {
				return err
			}

			for _, candidate := range candidates {
				claim, err := claimJournalAssetTx(
					ctx, tx, source.kind, source.table,
					candidate.assetID, candidate.fileID,
				)
				if err != nil {
					return err
				}
				claims = append(claims, claim)
			}
		}
		return nil
	})
	return claims, err
}

func claimJournalAssetTx(
	ctx context.Context,
	tx *sql.Tx,
	kind, table, assetID, fileID string,
) (JournalAssetPurge, error) {
	claim := JournalAssetPurge{Kind: kind, AssetID: assetID, FileID: fileID}
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET lifecycle_state='deleting' WHERE id=$1
			 AND lifecycle_state IN ('unreferenced','deleting')`,
			table,
		),
		assetID,
	); err != nil {
		return claim, err
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE space_storage_contributions
		 SET state='released',released_at=COALESCE(released_at,NOW()),updated_at=NOW()
		 WHERE source_kind=$1 AND source_id=$2 AND state IN ('active','recovery')`,
		kind, assetID,
	); err != nil {
		return claim, err
	}

	var domainID, sha, blobState, fileState string
	var byteSize int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT b.id,b.security_domain_id,b.sha256,b.byte_size,
		        b.r2_object_key,b.lifecycle_state,f.lifecycle_state
		 FROM library_files f
		 JOIN library_blobs b ON b.id=f.blob_id
		 WHERE f.id=$1
		 FOR UPDATE OF f,b`,
		fileID,
	).Scan(
		&claim.BlobID, &domainID, &sha, &byteSize,
		&claim.ObjectKey, &blobState, &fileState,
	); err != nil {
		return claim, err
	}
	if _, err := tx.ExecContext(
		ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		"library:blob:"+domainID+":"+sha+fmt.Sprint(byteSize),
	); err != nil {
		return claim, err
	}

	if fileState == "ready" {
		result, err := tx.ExecContext(
			ctx,
			`UPDATE library_files f
			 SET lifecycle_state='purging',version=version+1,updated_at=NOW()
			 WHERE f.id=$1 AND f.lifecycle_state='ready'
			   AND NOT EXISTS (
			       SELECT 1 FROM library_legal_holds h
			       WHERE h.active AND h.target_kind='file' AND h.target_id=f.id
			   )
			   AND NOT EXISTS (
			       SELECT 1 FROM space_library_items i
			       WHERE i.file_id=f.id AND i.lifecycle_state<>'deleted'
			   )
			   AND NOT EXISTS (
			       SELECT 1 FROM space_message_attachments a
			       WHERE a.file_id=f.id AND a.lifecycle_state<>'deleted'
			   )
			   AND NOT EXISTS (
			       SELECT 1 FROM space_note_assets a
			       WHERE a.file_id=f.id AND a.lifecycle_state='ready'
			   )
			   AND NOT EXISTS (
			       SELECT 1 FROM space_drawing_assets a
			       WHERE a.file_id=f.id AND a.lifecycle_state='ready'
			   )`,
			fileID,
		)
		if err != nil {
			return claim, err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			claim.ObjectKey = ""
			return claim, nil
		}
		fileState = "purging"
	}

	if fileState != "purging" {
		claim.ObjectKey = ""
		return claim, nil
	}
	if blobState == "purging" {
		claim.DeleteBlob = true
		return claim, nil
	}
	if blobState != "ready" {
		claim.ObjectKey = ""
		return claim, nil
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE library_blobs b
		 SET lifecycle_state='purging',version=version+1,updated_at=NOW()
		 WHERE b.id=$1 AND b.lifecycle_state='ready'
		   AND NOT EXISTS (
		       SELECT 1 FROM library_legal_holds h
		       WHERE h.active AND h.target_kind='blob' AND h.target_id=b.id
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM library_files f
		       WHERE f.blob_id=b.id AND f.id<>$2
		         AND f.lifecycle_state NOT IN ('purging','deleted')
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM library_item_versions v
		       WHERE v.rendition_blob_id=b.id AND v.lifecycle_state<>'deleted'
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM library_derivatives d
		       WHERE d.derivative_blob_id=b.id AND d.lifecycle_state<>'deleted'
		   )
		   AND NOT EXISTS (
		       SELECT 1 FROM library_exports e
		       WHERE e.export_blob_id=b.id AND e.state<>'deleted'
		   )`,
		claim.BlobID, fileID,
	)
	if err != nil {
		return claim, err
	}
	changed, _ := result.RowsAffected()
	claim.DeleteBlob = changed > 0
	if !claim.DeleteBlob {
		claim.ObjectKey = ""
	}
	return claim, nil
}

// CompleteJournalAssetPurge finalizes database lifecycle state after the
// caller has deleted ObjectKey from R2 (or when the blob was shared).
func (db *Database) CompleteJournalAssetPurge(
	ctx context.Context,
	claim JournalAssetPurge,
) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		table := "space_note_assets"
		if claim.Kind == JournalAssetKindDrawing {
			table = "space_drawing_assets"
		} else if claim.Kind != JournalAssetKindNote {
			return ErrLibraryInvalid
		}
		if claim.DeleteBlob {
			result, err := tx.ExecContext(
				ctx,
				`UPDATE library_blobs
				 SET lifecycle_state='deleted',deleted_at=NOW(),
				     version=version+1,updated_at=NOW()
				 WHERE id=$1 AND lifecycle_state='purging' AND r2_object_key=$2`,
				claim.BlobID, claim.ObjectKey,
			)
			if err != nil {
				return err
			}
			if changed, _ := result.RowsAffected(); changed == 0 {
				return ErrLibraryConflict
			}
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE library_files
			 SET lifecycle_state='deleted',deleted_at=NOW(),
			     version=version+1,updated_at=NOW()
			 WHERE id=$1 AND lifecycle_state='purging'`,
			claim.FileID,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET lifecycle_state='deleted',deleted_at=NOW()
				 WHERE id=$1 AND lifecycle_state='deleting'`,
				table,
			),
			claim.AssetID,
		)
		return err
	})
}
