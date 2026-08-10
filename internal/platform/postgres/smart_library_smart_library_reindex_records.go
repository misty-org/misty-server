package db

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// SmartLibraryReindexRecords verifies that every submitted asset belongs to the
// authenticated job and still has the same fingerprint. It returns the stored
// rich metadata used as the text side of the multimodal embedding.
func (db *Database) SmartLibraryReindexRecords(userID, jobID string, refs []SmartLibraryPreviewRef) (*SmartLibraryReindexJob, []SmartLibraryReindexAsset, error) {
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	var job SmartLibraryReindexJob
	var encoded []byte
	err = tx.QueryRow(`SELECT id,status,embedding_model,embedding_version,asset_ids,cursor,requested_assets,completed_assets,failed_assets FROM smart_library_reindex_jobs WHERE id=$1 AND user_id=$2 FOR UPDATE`, jobID, userID).Scan(&job.ID, &job.Status, &job.Model, &job.Version, &encoded, &job.Cursor, &job.Requested, &job.Completed, &job.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSmartLibraryNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var planned []SmartLibraryReindexAsset
	if err = json.Unmarshal(encoded, &planned); err != nil {
		return nil, nil, err
	}
	allowed := map[string]SmartLibraryReindexAsset{}
	for _, a := range planned {
		allowed[a.FolderID+"\x00"+a.AssetID] = a
	}
	selected := make([]SmartLibraryReindexAsset, 0, len(refs))
	for _, ref := range refs {
		matches := []SmartLibraryReindexAsset{}
		for _, a := range planned {
			if a.AssetID == ref.AssetID && a.Fingerprint == ref.Fingerprint {
				matches = append(matches, a)
			}
		}
		if len(matches) != 1 {
			return nil, nil, errors.New("asset is not in reindex job")
		}
		a := allowed[matches[0].FolderID+"\x00"+matches[0].AssetID]
		result, claimErr := tx.Exec(`UPDATE smart_library_assets SET index_status='processing',index_claim_token=$1,index_claimed_at=NOW(),index_failure_code=NULL,updated_at=NOW()
			WHERE user_id=$2 AND folder_id=$3 AND asset_id=$4 AND fingerprint=$5 AND status='analyzed'
			AND (semantic_embedding IS NULL OR embedding_model<>$6 OR embedding_version<>$7)
			AND (index_status<>'processing' OR index_claimed_at<NOW()-INTERVAL '15 minutes')`, jobID, userID, a.FolderID, a.AssetID, a.Fingerprint, job.Model, job.Version)
		if claimErr != nil {
			return nil, nil, claimErr
		}
		if claimed, _ := result.RowsAffected(); claimed == 1 {
			selected = append(selected, a)
		}
	}
	if len(selected) > 0 && job.Status == "pending" {
		job.Status = "processing"
		if _, err = tx.Exec(`UPDATE smart_library_reindex_jobs SET status='processing',updated_at=NOW() WHERE id=$1`, job.ID); err != nil {
			return nil, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &job, selected, nil
}

func (db *Database) CompleteSmartLibraryReindexJob(userID, jobID string, embeddings map[string]SmartLibraryCompletion, failures map[string]string) (*SmartLibraryReindexJob, error) {
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var job SmartLibraryReindexJob
	var encoded, completedEncoded, failedEncoded []byte
	if err = tx.QueryRow(`SELECT id,status,embedding_model,embedding_version,asset_ids,completed_asset_ids,failed_asset_ids,cursor,requested_assets,completed_assets,failed_assets FROM smart_library_reindex_jobs WHERE id=$1 AND user_id=$2 FOR UPDATE`, jobID, userID).Scan(&job.ID, &job.Status, &job.Model, &job.Version, &encoded, &completedEncoded, &failedEncoded, &job.Cursor, &job.Requested, &job.Completed, &job.Failed); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSmartLibraryNotFound
	} else if err != nil {
		return nil, err
	}
	var planned []SmartLibraryReindexAsset
	if err = json.Unmarshal(encoded, &planned); err != nil {
		return nil, err
	}
	allowed := map[string]SmartLibraryReindexAsset{}
	for _, a := range planned {
		allowed[a.FolderID+"\x00"+a.AssetID] = a
	}
	var completedKeys, failedKeys []string
	_ = json.Unmarshal(completedEncoded, &completedKeys)
	_ = json.Unmarshal(failedEncoded, &failedKeys)
	completedSet, failedSet := map[string]bool{}, map[string]bool{}
	for _, key := range completedKeys {
		completedSet[key] = true
	}
	for _, key := range failedKeys {
		failedSet[key] = true
	}
	for key, completion := range embeddings {
		storedKey := reindexStorageKey(key)
		a, ok := allowed[key]
		if !ok {
			return nil, errors.New("unexpected reindex asset")
		}
		vector, vectorErr := smartLibraryVector(completion.Embedding)
		if vectorErr != nil {
			return nil, vectorErr
		}
		metadataRefresh := completion.Description != ""
		tags, _ := json.Marshal(completion.Tags)
		collections, _ := json.Marshal(completion.Collections)
		metadata, _ := json.Marshal(completion.Metadata)
		result, execErr := tx.Exec(`UPDATE smart_library_assets SET
			semantic_embedding=$1::vector,embedding_model=$2,embedding_version=$3,embedding_input_hash=$4,embedded_at=NOW(),
			description=CASE WHEN $5 THEN $6 ELSE description END,
			tags=CASE WHEN $5 THEN $7::jsonb ELSE tags END,
			collections=CASE WHEN $5 THEN $8::jsonb ELSE collections END,
			confidence=CASE WHEN $5 THEN $9 ELSE confidence END,
			model=CASE WHEN $5 THEN $10 ELSE model END,
			metadata=CASE WHEN $5 THEN $11::jsonb ELSE metadata END,
			result_sequence=CASE WHEN $5 THEN nextval('smart_library_result_sequence') ELSE result_sequence END,
			analyzed_at=CASE WHEN $5 THEN NOW() ELSE analyzed_at END,
			index_status='indexed',index_failure_code=NULL,index_claim_token=NULL,index_claimed_at=NULL,updated_at=NOW()
			WHERE user_id=$12 AND folder_id=$13 AND asset_id=$14 AND fingerprint=$15 AND index_status='processing' AND index_claim_token=$16`,
			vector, job.Model, job.Version, completion.EmbeddingInputHash,
			metadataRefresh, completion.Description, tags, collections, completion.Confidence,
			completion.Model, metadata,
			userID, a.FolderID, a.AssetID, a.Fingerprint, job.ID)
		if execErr != nil {
			return nil, execErr
		}
		n, _ := result.RowsAffected()
		indexed := n == 1
		if !indexed {
			_ = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM smart_library_assets WHERE user_id=$1 AND folder_id=$2 AND asset_id=$3 AND fingerprint=$4 AND embedding_model=$5 AND embedding_version=$6 AND index_status='indexed')`, userID, a.FolderID, a.AssetID, a.Fingerprint, job.Model, job.Version).Scan(&indexed)
		}
		if indexed || completedSet[storedKey] {
			completedSet[storedKey] = true
			delete(failedSet, storedKey)
		}
	}
	for key, code := range failures {
		storedKey := reindexStorageKey(key)
		a, ok := allowed[key]
		if !ok {
			return nil, errors.New("unexpected reindex failure")
		}
		result, execErr := tx.Exec(`UPDATE smart_library_assets SET index_status='failed',index_failure_code=$1,index_claim_token=NULL,index_claimed_at=NULL,updated_at=NOW() WHERE user_id=$2 AND folder_id=$3 AND asset_id=$4 AND fingerprint=$5 AND index_status='processing' AND index_claim_token=$6`, code, userID, a.FolderID, a.AssetID, a.Fingerprint, job.ID)
		if execErr != nil {
			return nil, execErr
		}
		n, _ := result.RowsAffected()
		if (n == 1 || failedSet[storedKey]) && !completedSet[storedKey] {
			failedSet[storedKey] = true
		}
	}
	completedKeys = completedKeys[:0]
	for key := range completedSet {
		completedKeys = append(completedKeys, key)
	}
	failedKeys = failedKeys[:0]
	for key := range failedSet {
		failedKeys = append(failedKeys, key)
	}
	job.Completed = len(completedKeys)
	job.Failed = len(failedKeys)
	completedEncoded, _ = json.Marshal(completedKeys)
	failedEncoded, _ = json.Marshal(failedKeys)
	job.Status = "processing"
	if job.Completed+job.Failed >= job.Requested {
		job.Status = "completed"
		if job.Failed > 0 {
			job.Status = "partially_failed"
		}
	}
	if _, err = tx.Exec(`UPDATE smart_library_reindex_jobs SET status=$1,completed_assets=$2,failed_assets=$3,completed_asset_ids=$4,failed_asset_ids=$5,updated_at=NOW() WHERE id=$6`, job.Status, job.Completed, job.Failed, completedEncoded, failedEncoded, job.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func (db *Database) SmartLibraryBatches(userID, folderID string) ([]SmartLibraryBatch, error) {
	if _, err := db.SmartLibraryFolder(userID, folderID); err != nil {
		return nil, err
	}
	batches := []SmartLibraryBatch{}
	err := db.smartLibraryRows(`SELECT id,kind,status,asset_ids,successful_images,failed_images FROM smart_library_batches WHERE folder_id=$1 ORDER BY created_at`, []any{folderID}, func(rows *sql.Rows) error {
		for rows.Next() {
			var batch SmartLibraryBatch
			var ids []byte
			if err := rows.Scan(&batch.ID, &batch.Kind, &batch.Status, &ids, &batch.SuccessfulImages, &batch.FailedImages); err != nil {
				return err
			}
			_ = json.Unmarshal(ids, &batch.AssetIDs)
			batches = append(batches, batch)
		}
		return rows.Err()
	})
	return batches, err
}

func (db *Database) SmartLibrarySampleAssetIDs(userID, folderID string) ([]string, error) {
	if _, err := db.SmartLibraryFolder(userID, folderID); err != nil {
		return nil, err
	}
	ids := []string{}
	err := db.smartLibraryRows(`SELECT asset_id FROM smart_library_assets WHERE folder_id=$1 AND sample_eligible=TRUE ORDER BY asset_id`, []any{folderID}, func(rows *sql.Rows) error {
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

func (db *Database) RecoverStaleSmartLibraryBatches(userID, folderID string) error {
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE smart_library_assets a SET status='pending',updated_at=NOW() FROM smart_library_batches b,smart_library_folders f WHERE b.folder_id=a.folder_id AND f.id=b.folder_id AND f.user_id=$1 AND f.id=$2 AND b.status='processing' AND b.updated_at<NOW()-INTERVAL '15 minutes' AND b.asset_ids ? a.asset_id AND a.status='processing'`, userID, folderID); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE smart_library_batches b SET status='failed',updated_at=NOW() FROM smart_library_folders f WHERE f.id=b.folder_id AND f.user_id=$1 AND f.id=$2 AND b.status='processing' AND b.updated_at<NOW()-INTERVAL '15 minutes'`, userID, folderID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count > 0 {
		if _, err = tx.Exec(`UPDATE smart_library_folders SET state=CASE WHEN EXISTS(SELECT 1 FROM smart_library_assets WHERE folder_id=$1 AND status='analyzed') THEN 'sample_review' ELSE 'preflight' END,updated_at=NOW() WHERE id=$1 AND user_id=$2`, folderID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
