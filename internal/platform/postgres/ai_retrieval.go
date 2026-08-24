package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

type AIRetrievalHit struct {
	DocumentID     string
	SourceKind     string
	SourceID       string
	SpaceID        string
	SourceRevision string
	Title          string
	Href           string
	Content        string
	Score          float64
	LexicalScore   float64
	SemanticScore  float64
}

type AIEmbeddingChunk struct {
	DocumentID  string
	Ordinal     int
	Content     string
	ContentHash string
}

func (db *Database) PendingAIEmbeddingChunks(ctx context.Context, limit int) ([]AIEmbeddingChunk, error) {
	if limit < 1 || limit > 100 {
		limit = 32
	}
	items := []AIEmbeddingChunk{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT c.document_id,c.ordinal,c.content,c.content_hash
			FROM ai_retrieval_chunks c JOIN ai_retrieval_documents d ON d.id=c.document_id
			WHERE d.lifecycle_state='active' AND c.embedding IS NULL
			ORDER BY c.updated_at,c.document_id,c.ordinal LIMIT $1
		`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIEmbeddingChunk
			if err := rows.Scan(&item.DocumentID, &item.Ordinal, &item.Content, &item.ContentHash); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CompleteAIEmbeddingChunk(ctx context.Context, chunk AIEmbeddingChunk, embedding []float64, model string) error {
	if len(embedding) != 768 || strings.TrimSpace(model) == "" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE ai_retrieval_chunks SET embedding=$1::vector,embedding_model=$2,updated_at=NOW()
			WHERE document_id=$3 AND ordinal=$4 AND content_hash=$5 AND embedding IS NULL
		`, aiVectorLiteral(embedding), model, chunk.DocumentID, chunk.Ordinal, chunk.ContentHash)
		return err
	})
}

func (db *Database) SearchAIRetrieval(ctx context.Context, userID, query string, embedding []float64, limit int) ([]AIRetrievalHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []AIRetrievalHit{}, nil
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AIRetrievalHit{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var rows *sql.Rows
		var err error
		if len(embedding) == 768 {
			rows, err = tx.QueryContext(ctx, `
				WITH lexical_query AS (SELECT plainto_tsquery('simple',$1) value)
				SELECT d.id,d.source_kind,d.source_id,COALESCE(d.space_id,''),d.source_revision,d.title,d.href,c.content,
					(ts_rank_cd(c.lexical,q.value)*0.55 + CASE WHEN c.embedding IS NULL THEN 0 ELSE (1-(c.embedding <=> $2::vector))*0.45 END) score,
					ts_rank_cd(c.lexical,q.value) lexical_score,
					CASE WHEN c.embedding IS NULL THEN 0 ELSE (1-(c.embedding <=> $2::vector)) END semantic_score
				FROM ai_retrieval_documents d JOIN ai_retrieval_chunks c ON c.document_id=d.id CROSS JOIN lexical_query q
				WHERE d.lifecycle_state='active'
				  AND (
				    (d.privacy_class='private' AND d.owner_user_id=$4) OR
				    (d.privacy_class IN ('shared','provider')
				      AND misty_can_access_space_audience(d.space_id,d.audience_kind,d.audience_conversation_id)
				      AND (d.privacy_class='shared' OR
				        (d.source_kind='provider' AND EXISTS(
				          SELECT 1 FROM provider_content_records p JOIN provider_shared_resources r ON r.id=p.shared_resource_id
				          WHERE p.id=d.source_id AND p.deleted_at IS NULL AND r.status='active')) OR
				        (d.source_kind='calendar' AND EXISTS(
				          SELECT 1 FROM space_calendar_events e JOIN space_calendar_sources s ON s.id=e.source_id
				          WHERE e.id=d.source_id AND e.removed_at IS NULL AND s.status='active'))))
				  )
				  AND (c.lexical @@ q.value OR c.embedding IS NOT NULL)
				ORDER BY score DESC,d.updated_at DESC,d.id,c.ordinal LIMIT $3
			`, query, aiVectorLiteral(embedding), limit, userID)
		} else {
			rows, err = tx.QueryContext(ctx, `
				WITH lexical_query AS (SELECT plainto_tsquery('simple',$1) value)
				SELECT d.id,d.source_kind,d.source_id,COALESCE(d.space_id,''),d.source_revision,d.title,d.href,c.content,
					ts_rank_cd(c.lexical,q.value) score,ts_rank_cd(c.lexical,q.value) lexical_score,0::float8 semantic_score
				FROM ai_retrieval_documents d JOIN ai_retrieval_chunks c ON c.document_id=d.id CROSS JOIN lexical_query q
				WHERE d.lifecycle_state='active'
				  AND (
				    (d.privacy_class='private' AND d.owner_user_id=$3) OR
				    (d.privacy_class IN ('shared','provider')
				      AND misty_can_access_space_audience(d.space_id,d.audience_kind,d.audience_conversation_id)
				      AND (d.privacy_class='shared' OR
				        (d.source_kind='provider' AND EXISTS(
				          SELECT 1 FROM provider_content_records p JOIN provider_shared_resources r ON r.id=p.shared_resource_id
				          WHERE p.id=d.source_id AND p.deleted_at IS NULL AND r.status='active')) OR
				        (d.source_kind='calendar' AND EXISTS(
				          SELECT 1 FROM space_calendar_events e JOIN space_calendar_sources s ON s.id=e.source_id
				          WHERE e.id=d.source_id AND e.removed_at IS NULL AND s.status='active'))))
				  )
				  AND c.lexical @@ q.value
				ORDER BY score DESC,d.updated_at DESC,d.id,c.ordinal LIMIT $2
			`, query, limit, userID)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIRetrievalHit
			if err := rows.Scan(&item.DocumentID, &item.SourceKind, &item.SourceID, &item.SpaceID, &item.SourceRevision, &item.Title, &item.Href, &item.Content, &item.Score, &item.LexicalScore, &item.SemanticScore); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// RecentAIRetrieval supplies scheduled briefings without weakening the search
// boundary: authorization and provider lifecycle checks are part of candidate
// selection, before recency ranking or limiting.
func (db *Database) RecentAIRetrieval(ctx context.Context, userID string, limit int) ([]AIRetrievalHit, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AIRetrievalHit{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT d.id,d.source_kind,d.source_id,COALESCE(d.space_id,''),d.source_revision,d.title,d.href,c.content,0::float8,0::float8,0::float8
			FROM ai_retrieval_documents d JOIN ai_retrieval_chunks c ON c.document_id=d.id
			WHERE d.lifecycle_state='active'
			  AND (
			    (d.privacy_class='private' AND d.owner_user_id=$2) OR
			    (d.privacy_class IN ('shared','provider')
			      AND misty_can_access_space_audience(d.space_id,d.audience_kind,d.audience_conversation_id)
			      AND (d.privacy_class='shared' OR
			        (d.source_kind='provider' AND EXISTS(
			          SELECT 1 FROM provider_content_records p JOIN provider_shared_resources r ON r.id=p.shared_resource_id
			          WHERE p.id=d.source_id AND p.deleted_at IS NULL AND r.status='active')) OR
			        (d.source_kind='calendar' AND EXISTS(
			          SELECT 1 FROM space_calendar_events e JOIN space_calendar_sources s ON s.id=e.source_id
			          WHERE e.id=d.source_id AND e.removed_at IS NULL AND s.status='active'))))
			  )
			ORDER BY d.updated_at DESC,d.id,c.ordinal LIMIT $1
		`, limit, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIRetrievalHit
			if err := rows.Scan(&item.DocumentID, &item.SourceKind, &item.SourceID, &item.SpaceID, &item.SourceRevision, &item.Title, &item.Href, &item.Content, &item.Score, &item.LexicalScore, &item.SemanticScore); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func aiVectorLiteral(values []float64) string {
	parts := make([]string, len(values))
	for index, value := range values {
		if value > 1_000_000 || value < -1_000_000 {
			value = 0
		}
		parts[index] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ","))
}
