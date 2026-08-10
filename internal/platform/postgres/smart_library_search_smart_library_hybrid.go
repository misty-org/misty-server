package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// SearchSmartLibraryHybrid securely scopes candidates before rank fusion. The
// lexical branch preserves exact names and tags, while the ANN branch recovers
// concepts that the generated caption omitted.
func (db *Database) SearchSmartLibraryHybrid(userID, folderID, query string, embedding []float64, limit int) ([]SmartLibrarySearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []SmartLibrarySearchHit{}, nil
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	if folderID != "" {
		if _, err := db.SmartLibraryFolder(userID, folderID); err != nil {
			return nil, err
		}
	}
	var vector any
	if len(embedding) > 0 {
		formatted, err := smartLibraryVector(embedding)
		if err != nil {
			return nil, err
		}
		vector = formatted
	}
	candidateLimit := limit * 8
	if candidateLimit < 80 {
		candidateLimit = 80
	}
	querySQL := `
		WITH lexical AS (
			SELECT a.folder_id,a.asset_id,
				LEAST(1.0, ts_rank_cd(a.search_tsv, websearch_to_tsquery('simple'::regconfig,$3)) * 4.0
					+ CASE WHEN lower(COALESCE(a.description,'') || ' ' || a.tags::text || ' ' || a.metadata::text) LIKE '%' || lower($3) || '%' THEN 0.45 ELSE 0 END) AS lexical_score
			FROM smart_library_assets a
			WHERE a.user_id=$1 AND a.status='analyzed' AND ($2='' OR a.folder_id=$2)
				AND a.search_tsv @@ websearch_to_tsquery('simple'::regconfig,$3)
			ORDER BY lexical_score DESC
			LIMIT $5
		), semantic AS (
			SELECT a.folder_id,a.asset_id,GREATEST(0.0, 1.0-(a.semantic_embedding <=> $4::vector)) AS semantic_score
			FROM smart_library_assets a
			WHERE $4 IS NOT NULL AND a.user_id=$1 AND a.status='analyzed' AND a.semantic_embedding IS NOT NULL AND ($2='' OR a.folder_id=$2)
			ORDER BY a.semantic_embedding <=> $4::vector
			LIMIT $5
		), candidates AS (
			SELECT folder_id,asset_id FROM lexical UNION SELECT folder_id,asset_id FROM semantic
		), scored AS (
			SELECT a.*,
				COALESCE(l.lexical_score,0)::float8 AS lexical_score,
				COALESCE(s.semantic_score,0)::float8 AS semantic_score,
				(CASE WHEN $4 IS NULL THEN COALESCE(l.lexical_score,0)
					ELSE 0.68*COALESCE(s.semantic_score,0)+0.32*COALESCE(l.lexical_score,0) END)::float8 AS score
			FROM candidates c
			JOIN smart_library_assets a ON a.folder_id=c.folder_id AND a.asset_id=c.asset_id AND a.user_id=$1
			LEFT JOIN lexical l ON l.folder_id=a.folder_id AND l.asset_id=a.asset_id
			LEFT JOIN semantic s ON s.folder_id=a.folder_id AND s.asset_id=a.asset_id
		)
		SELECT folder_id,asset_id,asset_kind,mime_type,COALESCE(description,''),tags,collections,metadata,score,semantic_score,lexical_score
		FROM scored ORDER BY score DESC, analyzed_at DESC LIMIT $6`
	hits := []SmartLibrarySearchHit{}
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		// Strict iterative scans improve filtered HNSW recall without ever relaxing
		// the tenant predicate. Older pgvector releases simply reject this setting.
		_, _ = tx.Exec(`SET LOCAL hnsw.iterative_scan = 'strict_order'`)
		rows, err := tx.Query(querySQL, userID, folderID, query, vector, candidateLimit, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hit SmartLibrarySearchHit
			var tags, collections, metadata []byte
			if err := rows.Scan(&hit.FolderID, &hit.AssetID, &hit.AssetKind, &hit.MimeType, &hit.Description, &tags, &collections, &metadata, &hit.Score, &hit.SemanticScore, &hit.LexicalScore); err != nil {
				return err
			}
			_ = json.Unmarshal(tags, &hit.Tags)
			_ = json.Unmarshal(collections, &hit.Collections)
			_ = json.Unmarshal(metadata, &hit.Metadata)
			if hit.LexicalScore > 0 {
				hit.MatchReasons = append(hit.MatchReasons, "metadata")
			}
			if hit.SemanticScore > 0 {
				hit.MatchReasons = append(hit.MatchReasons, "semantic")
			}
			hits = append(hits, hit)
		}
		return rows.Err()
	})
	if err == nil {
		hits = TestingPruneWeakSemanticMatches(hits)
	}
	return hits, err
}

// Once the query has an exact metadata hit, low-scoring vector neighbors are
// usually visual lookalikes rather than useful answers. Preserve every lexical
// hit and only nearby semantic results; pure semantic searches retain their
// normal breadth.
func TestingPruneWeakSemanticMatches(hits []SmartLibrarySearchHit) []SmartLibrarySearchHit {
	if len(hits) < 2 {
		return hits
	}
	hasLexical := false
	for _, hit := range hits {
		if hit.LexicalScore > 0 {
			hasLexical = true
			break
		}
	}
	if !hasLexical {
		return hits
	}
	cutoff := hits[0].Score * 0.78
	filtered := make([]SmartLibrarySearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.LexicalScore > 0 || hit.Score >= cutoff {
			filtered = append(filtered, hit)
		}
	}
	return filtered
}

func (db *Database) RecordSmartLibraryCostEvent(userID, folderID, batchID, model string, batchSize int, inputTokens, outputTokens int64, success bool) error {
	_, err := db.smartLibraryExec(`INSERT INTO smart_library_cost_events(user_id,folder_id,batch_id,model,batch_size,input_tokens,output_tokens,success,event_kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'analysis')`, userID, folderID, batchID, model, batchSize, inputTokens, outputTokens, success)
	return err
}

func (db *Database) RecordSmartLibrarySemanticUsage(userID, folderID, eventKind, model string, batchSize int, inputTokens, outputTokens int64, success bool) error {
	if eventKind != "semantic_index" && eventKind != "semantic_query" && eventKind != "reindex" {
		return errors.New("invalid semantic usage kind")
	}
	var folder any
	if folderID != "" {
		folder = folderID
	}
	if batchSize < 1 {
		batchSize = 1
	}
	_, err := db.smartLibraryExec(`INSERT INTO smart_library_cost_events(user_id,folder_id,model,batch_size,input_tokens,output_tokens,success,event_kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, userID, folder, model, batchSize, inputTokens, outputTokens, success, eventKind)
	return err
}

func (db *Database) SmartLibrarySemanticCallsToday(userID, eventKind string) (int, error) {
	var count int
	err := db.smartLibraryScan(`SELECT COUNT(*) FROM smart_library_cost_events WHERE user_id=$1 AND event_kind=$2 AND created_at>=date_trunc('day',NOW())`, []any{userID, eventKind}, &count)
	return count, err
}

func (db *Database) SmartLibraryIndexStatusForUser(userID, folderID, model string, version int) (SmartLibraryIndexStatus, error) {
	status := SmartLibraryIndexStatus{CurrentVersion: version, EmbeddingModel: model}
	err := db.smartLibraryScan(`SELECT
		COUNT(*) FILTER (WHERE status='analyzed' AND (semantic_embedding IS NULL OR embedding_model<>$3 OR embedding_version<>$4)),
		COUNT(*) FILTER (WHERE status='analyzed' AND index_status='failed')
		FROM smart_library_assets WHERE user_id=$1 AND ($2='' OR folder_id=$2)`, []any{userID, folderID, model, version}, &status.OutdatedAssets, &status.FailedAssets)
	return status, err
}

func (db *Database) PlanSmartLibraryReindex(userID, folderID, cursor, model string, version, limit int) (*SmartLibraryReindexJob, error) {
	if folderID != "" {
		if _, err := db.SmartLibraryFolder(userID, folderID); err != nil {
			return nil, err
		}
	}
	if version < 1 || strings.TrimSpace(model) == "" {
		return nil, errors.New("invalid reindex target")
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	assets := []SmartLibraryReindexAsset{}
	err := db.smartLibraryRows(`SELECT asset_id,folder_id,fingerprint,asset_kind,mime_type,COALESCE(description,''),tags,collections,metadata
		FROM smart_library_assets WHERE user_id=$1 AND status='analyzed' AND ($2='' OR folder_id=$2)
		AND (folder_id||':'||asset_id)>$3 AND (semantic_embedding IS NULL OR embedding_model<>$4 OR embedding_version<>$5)
		AND (index_status<>'processing' OR index_claimed_at<NOW()-INTERVAL '15 minutes')
		ORDER BY folder_id,asset_id LIMIT $6`, []any{userID, folderID, cursor, model, version, limit}, func(rows *sql.Rows) error {
		for rows.Next() {
			var asset SmartLibraryReindexAsset
			var tags, collections, metadata []byte
			if err := rows.Scan(&asset.AssetID, &asset.FolderID, &asset.Fingerprint, &asset.AssetKind, &asset.MimeType, &asset.Description, &tags, &collections, &metadata); err != nil {
				return err
			}
			_ = json.Unmarshal(tags, &asset.Tags)
			_ = json.Unmarshal(collections, &asset.Collections)
			_ = json.Unmarshal(metadata, &asset.Metadata)
			asset.RequiresPreview = asset.AssetKind == "image" || asset.AssetKind == "document"
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	job := &SmartLibraryReindexJob{ID: "slreindex_" + uuid.NewString(), Status: "pending", Model: model, Version: version, Requested: len(assets), Assets: assets, Cursor: cursor}
	if len(assets) == 0 {
		job.Status = "completed"
	}
	if len(assets) > 0 {
		last := assets[len(assets)-1]
		job.Cursor = last.FolderID + ":" + last.AssetID
	}
	encoded, _ := json.Marshal(assets)
	var nullableFolder any
	if folderID != "" {
		nullableFolder = folderID
	}
	_, err = db.smartLibraryExec(`INSERT INTO smart_library_reindex_jobs(id,user_id,folder_id,status,embedding_model,embedding_version,asset_ids,cursor,requested_assets) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, job.ID, userID, nullableFolder, job.Status, model, version, encoded, job.Cursor, len(assets))
	return job, err
}
