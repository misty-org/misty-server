package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const CreditMeterMediaSearchMinute = "media_search_minute"

const LegacyMediaSearchDeviceID = "device_00000000000000000000000000000000"

type MediaSearchAsset struct {
	UserID           string `json:"-"`
	DeviceID         string `json:"deviceId"`
	AssetID          string `json:"assetId"`
	Fingerprint      string `json:"fingerprint"`
	MediaType        string `json:"mediaType"`
	MimeType         string `json:"mimeType"`
	Status           string `json:"status"`
	DurationMS       int64  `json:"durationMs"`
	IndexedThroughMS int64  `json:"indexedThroughMs"`
}

type MediaSearchSegment struct {
	ID, AssetID, Kind, Content, Transcript, VisualDescription, EmbeddingModel string
	ChunkIndex                                                                int
	StartMS, EndMS                                                            int64
	VisibleText                                                               []string
	Metadata                                                                  map[string]any
	Embedding                                                                 []float64
}

type MediaSearchHit struct {
	SegmentID         string   `json:"segmentId"`
	AssetID           string   `json:"assetId"`
	MediaType         string   `json:"mediaType"`
	Kind              string   `json:"kind"`
	Content           string   `json:"content"`
	Transcript        string   `json:"transcript"`
	VisualDescription string   `json:"visualDescription"`
	StartMS           int64    `json:"startMs"`
	EndMS             int64    `json:"endMs"`
	VisibleText       []string `json:"visibleText"`
	Score             float64  `json:"score"`
	SemanticScore     float64  `json:"semanticScore"`
	LexicalScore      float64  `json:"lexicalScore"`
}

var ErrMediaChunkBusy = errors.New("media chunk is already processing")

func (db *Database) ClaimMediaSearchChunk(userID string, asset MediaSearchAsset, chunkIndex int, startMS, endMS int64) (bool, error) {
	claimed := false
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO media_search_devices(user_id,device_id,last_seen_at) VALUES($1,$2,NOW()) ON CONFLICT(user_id,device_id) DO UPDATE SET last_seen_at=NOW()`, userID, asset.DeviceID); err != nil {
			return err
		}
		var priorFingerprint string
		priorErr := tx.QueryRow(`SELECT fingerprint FROM media_search_assets WHERE user_id=$1 AND device_id=$2 AND asset_id=$3 FOR UPDATE`, userID, asset.DeviceID, asset.AssetID).Scan(&priorFingerprint)
		if priorErr != nil && !errors.Is(priorErr, sql.ErrNoRows) {
			return priorErr
		}
		_, err := tx.Exec(`INSERT INTO media_search_assets(user_id,device_id,asset_id,fingerprint,media_type,mime_type,duration_ms,status) VALUES($1,$2,$3,$4,$5,$6,$7,'processing') ON CONFLICT(user_id,device_id,asset_id) DO UPDATE SET fingerprint=excluded.fingerprint,media_type=excluded.media_type,mime_type=excluded.mime_type,duration_ms=excluded.duration_ms,status=CASE WHEN media_search_assets.fingerprint<>excluded.fingerprint THEN 'processing' ELSE media_search_assets.status END,indexed_through_ms=CASE WHEN media_search_assets.fingerprint<>excluded.fingerprint THEN 0 ELSE media_search_assets.indexed_through_ms END,updated_at=NOW()`, userID, asset.DeviceID, asset.AssetID, asset.Fingerprint, asset.MediaType, asset.MimeType, asset.DurationMS)
		if err != nil {
			return err
		}
		if priorFingerprint != "" && priorFingerprint != asset.Fingerprint {
			if _, err = tx.Exec(`DELETE FROM media_search_chunks WHERE user_id=$1 AND device_id=$2 AND asset_id=$3`, userID, asset.DeviceID, asset.AssetID); err != nil {
				return err
			}
		}
		result, err := tx.Exec(`INSERT INTO media_search_chunks(user_id,device_id,asset_id,chunk_index,fingerprint,start_ms,end_ms,status) VALUES($1,$2,$3,$4,$5,$6,$7,'processing') ON CONFLICT(user_id,device_id,asset_id,chunk_index) DO NOTHING`, userID, asset.DeviceID, asset.AssetID, chunkIndex, asset.Fingerprint, startMS, endMS)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 1 {
			claimed = true
			return nil
		}
		var status, fingerprint string
		var leaseExpired bool
		if err = tx.QueryRow(`SELECT status,fingerprint,updated_at < NOW()-INTERVAL '10 minutes' FROM media_search_chunks WHERE user_id=$1 AND device_id=$2 AND asset_id=$3 AND chunk_index=$4 FOR UPDATE`, userID, asset.DeviceID, asset.AssetID, chunkIndex).Scan(&status, &fingerprint, &leaseExpired); err != nil {
			return err
		}
		if status == "indexed" && fingerprint == asset.Fingerprint {
			return nil
		}
		if status == "failed" || fingerprint != asset.Fingerprint || leaseExpired {
			if _, err = tx.Exec(`DELETE FROM media_search_segments WHERE user_id=$1 AND device_id=$2 AND asset_id=$3 AND chunk_index=$4`, userID, asset.DeviceID, asset.AssetID, chunkIndex); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE media_search_chunks SET fingerprint=$1,start_ms=$2,end_ms=$3,status='processing',failure_code=NULL,updated_at=NOW() WHERE user_id=$4 AND device_id=$5 AND asset_id=$6 AND chunk_index=$7`, asset.Fingerprint, startMS, endMS, userID, asset.DeviceID, asset.AssetID, chunkIndex); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE media_search_assets SET status='processing',updated_at=NOW() WHERE user_id=$1 AND device_id=$2 AND asset_id=$3`, userID, asset.DeviceID, asset.AssetID); err != nil {
				return err
			}
			claimed = true
			return nil
		}
		return ErrMediaChunkBusy
	})
	return claimed, err
}

func (db *Database) PruneIncompleteMediaSearchAssets(userID, deviceID string) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM media_search_assets WHERE user_id=$1 AND device_id=$2 AND status<>'indexed' AND updated_at < NOW()-INTERVAL '30 days'`, userID, deviceID)
		return err
	})
}

func (db *Database) CompleteMediaSearchChunk(userID, deviceID, assetID string, chunkIndex int, endMS int64, segments []MediaSearchSegment) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM media_search_segments WHERE user_id=$1 AND device_id=$2 AND asset_id=$3 AND chunk_index=$4`, userID, deviceID, assetID, chunkIndex); err != nil {
			return err
		}
		for _, segment := range segments {
			visible, _ := json.Marshal(segment.VisibleText)
			metadata, _ := json.Marshal(segment.Metadata)
			var vector any
			if len(segment.Embedding) > 0 {
				formatted, err := smartLibraryVector(segment.Embedding)
				if err != nil {
					return err
				}
				vector = formatted
			}
			id := segment.ID
			if id == "" {
				id = "mseg_" + uuid.NewString()
			}
			_, err := tx.Exec(`INSERT INTO media_search_segments(id,user_id,device_id,asset_id,chunk_index,start_ms,end_ms,segment_kind,content,transcript,visual_description,visible_text,metadata,embedding,embedding_model) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::vector,NULLIF($15,''))`, id, userID, deviceID, assetID, chunkIndex, segment.StartMS, segment.EndMS, segment.Kind, segment.Content, segment.Transcript, segment.VisualDescription, visible, metadata, vector, segment.EmbeddingModel)
			if err != nil {
				return err
			}
		}
		result, err := tx.Exec(`UPDATE media_search_chunks SET status='indexed',failure_code=NULL,updated_at=NOW() WHERE user_id=$1 AND device_id=$2 AND asset_id=$3 AND chunk_index=$4 AND status='processing'`, userID, deviceID, assetID, chunkIndex)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrMediaChunkBusy
		}
		rows, err := tx.Query(`SELECT chunk_index,end_ms,status FROM media_search_chunks WHERE user_id=$1 AND device_id=$2 AND asset_id=$3 ORDER BY chunk_index`, userID, deviceID, assetID)
		if err != nil {
			return err
		}
		contiguousIndex, indexedThrough := 0, int64(0)
		for rows.Next() {
			var index int
			var chunkEnd int64
			var status string
			if err = rows.Scan(&index, &chunkEnd, &status); err != nil {
				rows.Close()
				return err
			}
			if index != contiguousIndex || status != "indexed" {
				break
			}
			indexedThrough = chunkEnd
			contiguousIndex++
		}
		if err = rows.Close(); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE media_search_assets SET indexed_through_ms=$1,status=CASE WHEN $1>=duration_ms THEN 'indexed' ELSE 'processing' END,updated_at=NOW() WHERE user_id=$2 AND device_id=$3 AND asset_id=$4`, indexedThrough, userID, deviceID, assetID)
		return err
	})
}

func (db *Database) FailMediaSearchChunk(userID, deviceID, assetID string, chunkIndex int, code string) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE media_search_chunks SET status='failed',failure_code=$1,updated_at=NOW() WHERE user_id=$2 AND device_id=$3 AND asset_id=$4 AND chunk_index=$5`, code, userID, deviceID, assetID, chunkIndex); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE media_search_assets SET status='failed',updated_at=NOW() WHERE user_id=$1 AND device_id=$2 AND asset_id=$3`, userID, deviceID, assetID)
		return err
	})
}

func (db *Database) SearchMedia(userID, deviceID, query string, embedding []float64, limit int) ([]MediaSearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []MediaSearchHit{}, nil
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	var vector any
	if len(embedding) > 0 {
		formatted, err := smartLibraryVector(embedding)
		if err != nil {
			return nil, err
		}
		vector = formatted
	}
	sqlQuery := `WITH lexical AS (SELECT id,LEAST(1.0,ts_rank_cd(search_tsv,websearch_to_tsquery('simple',$3))*4.0+CASE WHEN lower(content) LIKE '%'||lower($3)||'%' THEN .45 ELSE 0 END) score FROM media_search_segments WHERE user_id=$1 AND device_id=$2 AND search_tsv@@websearch_to_tsquery('simple',$3) ORDER BY score DESC LIMIT $5), semantic AS (SELECT id,GREATEST(0.0,1.0-(embedding<=>$4::vector)) score FROM media_search_segments WHERE user_id=$1 AND device_id=$2 AND $4 IS NOT NULL AND embedding IS NOT NULL ORDER BY embedding<=>$4::vector LIMIT $5), candidates AS (SELECT id FROM lexical UNION SELECT id FROM semantic) SELECT s.id,s.asset_id,a.media_type,s.segment_kind,s.start_ms,s.end_ms,s.content,s.transcript,s.visual_description,s.visible_text,COALESCE(l.score,0),COALESCE(v.score,0),(CASE WHEN $4 IS NULL THEN COALESCE(l.score,0) ELSE .68*COALESCE(v.score,0)+.32*COALESCE(l.score,0) END) final_score FROM candidates c JOIN media_search_segments s ON s.id=c.id AND s.user_id=$1 AND s.device_id=$2 JOIN media_search_assets a ON a.user_id=s.user_id AND a.device_id=s.device_id AND a.asset_id=s.asset_id LEFT JOIN lexical l ON l.id=s.id LEFT JOIN semantic v ON v.id=s.id ORDER BY final_score DESC,s.start_ms LIMIT $6`
	hits := []MediaSearchHit{}
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, _ = tx.Exec(`SET LOCAL hnsw.iterative_scan='strict_order'`)
		rows, err := tx.Query(sqlQuery, userID, deviceID, query, vector, max(80, limit*8), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h MediaSearchHit
			var visible []byte
			if err = rows.Scan(&h.SegmentID, &h.AssetID, &h.MediaType, &h.Kind, &h.StartMS, &h.EndMS, &h.Content, &h.Transcript, &h.VisualDescription, &visible, &h.LexicalScore, &h.SemanticScore, &h.Score); err != nil {
				return err
			}
			_ = json.Unmarshal(visible, &h.VisibleText)
			hits = append(hits, h)
		}
		return rows.Err()
	})
	if err == nil {
		hits = pruneMediaSearchHits(hits)
	}
	return hits, err
}
