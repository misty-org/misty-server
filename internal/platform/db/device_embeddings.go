package db

import (
	"context"
	"database/sql"
)

// Embeddings are made on the person's desktop when agents run there
// (docs/agents/runtime.md): a desktop claims its owner's chunks that wait
// for a vector, embeds them on the device's model and completes them with the
// same lease checks the server's own worker used.

// PendingAIEmbeddingChunksFor leases one person's retrieval chunks that wait for a vector.
func (db *Database) PendingAIEmbeddingChunksFor(ctx context.Context, userID string, limit int) ([]AIEmbeddingChunk, error) {
	if limit < 1 || limit > 100 {
		limit = 32
	}
	items := []AIEmbeddingChunk{}
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
 WITH candidates AS (
 SELECT c.document_id,c.ordinal,d.owner_user_id
 FROM ai_retrieval_chunks c JOIN ai_retrieval_documents d ON d.id=c.document_id
 WHERE d.lifecycle_state='active' AND d.owner_user_id=$2 AND c.embedding IS NULL
 AND (c.embedding_lease_until IS NULL OR c.embedding_lease_until<=now())
 ORDER BY c.updated_at,c.document_id,c.ordinal LIMIT $1 FOR UPDATE OF c SKIP LOCKED
 ) UPDATE ai_retrieval_chunks c SET embedding_attempt_id=gen_random_uuid(),embedding_lease_until=now()+interval '5 minutes'
 FROM candidates p WHERE c.document_id=p.document_id AND c.ordinal=p.ordinal
 RETURNING c.document_id,c.ordinal,c.content,c.content_hash,p.owner_user_id,c.embedding_attempt_id
 `, limit, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIEmbeddingChunk
			if err := rows.Scan(&item.DocumentID, &item.Ordinal, &item.Content, &item.ContentHash, &item.AccountID, &item.AttemptID); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// PendingMemoryChunksFor leases one person's memory chunks that wait for a vector.
func (db *Database) PendingMemoryChunksFor(ctx context.Context, userID string, limit int) ([]MemoryChunk, error) {
	if limit < 1 || limit > 100 {
		limit = 32
	}
	items := []MemoryChunk{}
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
 WITH candidates AS (
  SELECT c.branch,c.record_id,c.chunk FROM memory_chunks c
  WHERE c.owner_user_id=$2 AND c.embedding IS NULL AND c.due_at<=now() AND (c.embedding_lease_until IS NULL OR c.embedding_lease_until<=now())
  ORDER BY c.due_at LIMIT $1 FOR UPDATE OF c SKIP LOCKED
 ) UPDATE memory_chunks c SET embedding_attempt_id=gen_random_uuid(),embedding_lease_until=now()+interval '5 minutes',
   embedding_attempts=c.embedding_attempts+1
 FROM candidates p WHERE c.branch=p.branch AND c.record_id=p.record_id AND c.chunk=p.chunk
 RETURNING c.owner_user_id,c.space_id,c.branch,c.record_id,c.chunk,c.text,c.content_hash,c.embedding_attempt_id`, limit, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item MemoryChunk
			if err := rows.Scan(&item.OwnerUserID, &item.SpaceID, &item.Branch, &item.RecordID, &item.Chunk, &item.Text, &item.ContentHash, &item.AttemptID); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
