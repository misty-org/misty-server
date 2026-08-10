package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) SetSmartLibraryAssetTags(userID, folderID, assetID string, tags []string) (SmartLibraryAssetResult, error) {
	encoded, err := json.Marshal(tags)
	if err != nil {
		return SmartLibraryAssetResult{}, err
	}
	var sequence int64
	err = db.smartLibraryScan(`UPDATE smart_library_assets SET tags=$1::jsonb,result_sequence=nextval('smart_library_result_sequence'),updated_at=NOW() WHERE user_id=$2 AND folder_id=$3 AND asset_id=$4 AND status='analyzed' RETURNING result_sequence`, []any{encoded, userID, folderID, assetID}, &sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return SmartLibraryAssetResult{}, ErrSmartLibraryNotFound
	}
	if err != nil {
		return SmartLibraryAssetResult{}, err
	}
	results, _, err := db.SmartLibraryResults(userID, folderID, sequence-1)
	if err != nil {
		return SmartLibraryAssetResult{}, err
	}
	for _, result := range results {
		if result.AssetID == assetID {
			return result, nil
		}
	}
	return SmartLibraryAssetResult{}, ErrSmartLibraryNotFound
}

func (db *Database) CreateSmartLibraryBatch(userID, folderID, kind string, previews []SmartLibraryPreviewRef) (*SmartLibraryBatch, error) {
	if kind != "sample" && kind != "full" {
		return nil, errors.New("invalid smart library batch kind")
	}
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var successful int
	if err = tx.QueryRow(`SELECT successful_images FROM smart_library_folders WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`, folderID, userID).Scan(&successful); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSmartLibraryNotFound
	} else if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(previews))
	for _, preview := range previews {
		var counted bool
		var row *sql.Row
		if kind == "sample" {
			row = tx.QueryRow(`UPDATE smart_library_assets SET status='processing',failure_code=NULL,updated_at=NOW() WHERE folder_id=$1 AND asset_id=$2 AND fingerprint=$3 AND sample_eligible=TRUE AND status IN ('pending','failed') RETURNING counted_success`, folderID, preview.AssetID, preview.Fingerprint)
		} else {
			assetKind, mimeType := normalizedAssetType(preview.AssetKind, preview.MimeType, "")
			row = tx.QueryRow(`INSERT INTO smart_library_assets(folder_id,user_id,asset_id,fingerprint,extension,size_bytes,modified_bucket,status,asset_kind,mime_type) VALUES($1,$2,$3,$4,'',0,0,'processing',$5,$6)
				ON CONFLICT(folder_id,asset_id) DO UPDATE SET fingerprint=excluded.fingerprint,status='processing',failure_code=NULL,asset_kind=excluded.asset_kind,mime_type=excluded.mime_type,semantic_embedding=NULL,embedding_model=NULL,embedding_version=0,embedding_input_hash=NULL,embedded_at=NULL,index_status='pending',index_failure_code=NULL,index_claim_token=NULL,index_claimed_at=NULL,updated_at=NOW()
				WHERE (smart_library_assets.status IN ('pending','failed') AND (NOT smart_library_assets.sample_eligible OR smart_library_assets.counted_success)) OR (smart_library_assets.status='analyzed' AND smart_library_assets.fingerprint<>excluded.fingerprint)
				RETURNING counted_success`, folderID, userID, preview.AssetID, preview.Fingerprint, assetKind, mimeType)
		}
		err := row.Scan(&counted)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("preview does not match an eligible asset")
		} else if err != nil {
			return nil, err
		}
		ids = append(ids, preview.AssetID)
	}
	_ = successful // retained for the row lock that serializes concurrent approvals.
	encoded, _ := json.Marshal(ids)
	batch := &SmartLibraryBatch{ID: "slbatch_" + uuid.NewString(), Kind: kind, Status: "processing", AssetIDs: ids}
	if _, err = tx.Exec(`INSERT INTO smart_library_batches(id,folder_id,kind,status,asset_ids)VALUES($1,$2,$3,'processing',$4)`, batch.ID, folderID, kind, encoded); err != nil {
		return nil, err
	}
	state := "full_processing"
	if kind == "sample" {
		state = "sample_processing"
	}
	if _, err = tx.Exec(`UPDATE smart_library_folders SET state=$1,updated_at=NOW() WHERE id=$2`, state, folderID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return batch, nil
}

func (db *Database) ResetSmartLibraryBatch(batchID string) error {
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var folderID string
	var encoded []byte
	if err = tx.QueryRow(`UPDATE smart_library_batches SET status='failed',updated_at=NOW() WHERE id=$1 RETURNING folder_id,asset_ids`, batchID).Scan(&folderID, &encoded); err != nil {
		return err
	}
	var ids []string
	_ = json.Unmarshal(encoded, &ids)
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE smart_library_assets SET status='pending',updated_at=NOW() WHERE folder_id=$1 AND asset_id=$2 AND status='processing'`, folderID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *Database) CompleteSmartLibraryBatch(userID, batchID string, completions []SmartLibraryCompletion, failures map[string]string, finalBatch bool) (*SmartLibraryFolder, error) {
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var folderID, kind string
	var encoded []byte
	if err = tx.QueryRow(`SELECT b.folder_id,b.kind,b.asset_ids FROM smart_library_batches b JOIN smart_library_folders f ON f.id=b.folder_id WHERE b.id=$1 AND f.user_id=$2 AND f.deleted_at IS NULL FOR UPDATE`, batchID, userID).Scan(&folderID, &kind, &encoded); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSmartLibraryNotFound
	} else if err != nil {
		return nil, err
	}
	var ids []string
	if err = json.Unmarshal(encoded, &ids); err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	seen := make(map[string]bool, len(ids))
	newSuccesses := 0
	for _, completion := range completions {
		if !allowed[completion.AssetID] || seen[completion.AssetID] {
			return nil, errors.New("analysis returned an unexpected asset")
		}
		seen[completion.AssetID] = true
		tags, _ := json.Marshal(completion.Tags)
		collections, _ := json.Marshal(completion.Collections)
		metadata, _ := json.Marshal(completion.Metadata)
		assetKind, mimeType := strings.TrimSpace(completion.AssetKind), strings.TrimSpace(completion.MimeType)
		if assetKind != "" || mimeType != "" {
			assetKind, mimeType = normalizedAssetType(assetKind, mimeType, "")
		}
		var embedding any
		indexStatus := "failed"
		indexFailure := "embedding_missing"
		if len(completion.Embedding) > 0 {
			vector, vectorErr := smartLibraryVector(completion.Embedding)
			if vectorErr != nil {
				return nil, vectorErr
			}
			embedding = vector
			indexStatus = "indexed"
			indexFailure = ""
		}
		var wasCounted bool
		if err := tx.QueryRow(`SELECT counted_success FROM smart_library_assets WHERE folder_id=$1 AND asset_id=$2 AND status='processing' FOR UPDATE`, folderID, completion.AssetID).Scan(&wasCounted); err != nil {
			return nil, err
		}
		result, err := tx.Exec(`UPDATE smart_library_assets SET status='analyzed',counted_success=TRUE,description=$1,tags=$2,collections=$3,confidence=$4,failure_code=NULL,model=$5,metadata=$6,asset_kind=COALESCE(NULLIF($7,''),asset_kind),mime_type=COALESCE(NULLIF($8,''),mime_type),semantic_embedding=$9::vector,embedding_model=NULLIF($10,''),embedding_version=$11,embedding_input_hash=NULLIF($12,''),embedded_at=CASE WHEN $9 IS NULL THEN NULL ELSE NOW() END,index_status=$13,index_failure_code=NULLIF($14,''),index_claim_token=NULL,index_claimed_at=NULL,result_sequence=nextval('smart_library_result_sequence'),analyzed_at=NOW(),updated_at=NOW() WHERE folder_id=$15 AND asset_id=$16 AND status='processing'`, completion.Description, tags, collections, completion.Confidence, completion.Model, metadata, assetKind, mimeType, embedding, completion.EmbeddingModel, completion.EmbeddingVersion, completion.EmbeddingInputHash, indexStatus, indexFailure, folderID, completion.AssetID)
		if err != nil {
			return nil, err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return nil, errors.New("analysis asset is no longer processing")
		}
		if !wasCounted {
			newSuccesses++
		}
	}
	for id, code := range failures {
		if !allowed[id] || seen[id] {
			return nil, errors.New("analysis failure returned an unexpected asset")
		}
		seen[id] = true
		if _, err = tx.Exec(`UPDATE smart_library_assets SET status='failed',failure_code=$1,result_sequence=nextval('smart_library_result_sequence'),updated_at=NOW() WHERE folder_id=$2 AND asset_id=$3 AND status='processing'`, code, folderID, id); err != nil {
			return nil, err
		}
	}
	if len(seen) != len(ids) {
		return nil, errors.New("analysis did not resolve every asset")
	}
	status := "completed"
	if len(completions) == 0 {
		status = "failed"
	} else if len(failures) > 0 {
		status = "partially_failed"
	}
	if _, err = tx.Exec(`UPDATE smart_library_batches SET status=$1,successful_images=$2,failed_images=$3,updated_at=NOW() WHERE id=$4`, status, len(completions), len(failures), batchID); err != nil {
		return nil, err
	}
	state := "full_processing"
	if kind == "sample" {
		state = "sample_processing"
	}
	if finalBatch {
		state = "complete"
		if kind == "sample" {
			state = "sample_review"
		}
	}
	result, err := tx.Exec(`UPDATE smart_library_folders SET state=$1,successful_images=successful_images+$2,failed_images=(SELECT COUNT(*) FROM smart_library_assets WHERE folder_id=$3 AND status='failed'),updated_at=NOW() WHERE id=$3`, state, newSuccesses, folderID)
	if err != nil {
		return nil, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return nil, ErrSmartLibraryNotFound
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return db.SmartLibraryFolder(userID, folderID)
}

func (db *Database) SearchSmartLibrary(userID, folderID, query string, limit int) ([]SmartLibrarySearchHit, error) {
	return db.SearchSmartLibraryHybrid(userID, folderID, query, nil, limit)
}
