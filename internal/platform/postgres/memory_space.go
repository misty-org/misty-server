package db

import (
	"context"
	"database/sql"
	"strings"
)

// The memory tree (docs/plans/spaces-as-profiles.md, phase 7): each person has
// one per Space, kept on the server, private to its owner and searched by
// meaning. Every memory row names its Space; RLS opens a row only for its
// owner while they belong to that Space.

// Memory branches as people and agents see them.
const (
	MemoryMoments   = "moments"
	MemorySummaries = "summaries"
	MemoryFacts     = "facts"
	MemoryWorkflows = "workflows"
)

// MemorySpace is the Space whose tree a request reads and writes: the one
// asked for when the person belongs to it, or their personal Space when none
// is named.
func (db *Database) MemorySpace(ctx context.Context, user, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if user == "" {
		return "", ErrSpaceInvalid
	}
	space := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var err error
		space, err = memorySpaceTx(ctx, tx, user, requested)
		return err
	})
	return space, err
}

// memorySpaceTx is space, or the owner's personal Space when space is empty:
// the store's rule for every memory read and write. A named Space must be one
// the owner belongs to; RLS and the Space guard trigger back this up.
func memorySpaceTx(ctx context.Context, tx *sql.Tx, user, space string) (string, error) {
	if space = strings.TrimSpace(space); space != "" {
		_, err := requireSpaceMemberTx(ctx, tx, space, user)
		return space, err
	}
	var personal sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT misty_ensure_personal_space($1)`, user).Scan(&personal); err != nil {
		return "", err
	}
	if !personal.Valid {
		return "", ErrSpaceNotFound
	}
	return personal.String, nil
}

// MemoryHit is one record found by meaning.
type MemoryHit struct {
	Branch   string  `json:"branch"`
	RecordID string  `json:"record_id"`
	Text     string  `json:"text"`
	Distance float64 `json:"distance"`
}

// SearchMemory ranks one person's records in one Space by meaning, nearest
// first. There is no word search: without a query vector there are no hits.
func (db *Database) SearchMemory(ctx context.Context, user, space string, branches []string, embedding []float64, model string, limit int) ([]MemoryHit, error) {
	hits := []MemoryHit{}
	if user == "" || space == "" || len(embedding) != 768 || model == "" || len(branches) == 0 {
		return hits, nil
	}
	if limit < 1 || limit > 50 {
		limit = 10
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		// Filtered HNSW scans keep looking past other people's rows on pgvector 0.8+.
		if _, err := tx.ExecContext(ctx, `DO $$ BEGIN
 IF (SELECT string_to_array(split_part(extversion,'-',1),'.')::int[] >= '{0,8}' FROM pg_extension WHERE extname='vector') THEN
  PERFORM set_config('hnsw.iterative_scan','relaxed_order',true);
 END IF;
END $$`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT branch,record_id,text,distance FROM (
 SELECT branch,record_id,text,embedding <=> $4::vector AS distance FROM memory_chunks
 WHERE owner_user_id=$1 AND space_id=$2 AND branch = ANY(string_to_array($3,',')) AND embedding IS NOT NULL AND embedding_model=$5
 ORDER BY embedding <=> $4::vector LIMIT $6) nearest ORDER BY distance`,
			user, space, strings.Join(branches, ","), aiVectorLiteral(embedding), model, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hit MemoryHit
			if err := rows.Scan(&hit.Branch, &hit.RecordID, &hit.Text, &hit.Distance); err != nil {
				return err
			}
			hits = append(hits, hit)
		}
		return rows.Err()
	})
	return hits, err
}

// MemoryChunk is one record's text waiting for its vector.
type MemoryChunk struct {
	OwnerUserID string
	SpaceID     string
	Branch      string
	RecordID    string
	Chunk       int
	Text        string
	ContentHash string
	AttemptID   string
}

// memoryChunkAttempts is how often a chunk is tried before it waits a day.
const memoryChunkAttempts = 5

// PendingMemoryChunks leases due chunks of one owner, so one embedding call
// carries a batch billed to one account. It is the background backfill too:
// rows that existed before the index wait here like new ones.
func (db *Database) PendingMemoryChunks(ctx context.Context, limit int) ([]MemoryChunk, error) {
	if limit < 1 || limit > 100 {
		limit = 32
	}
	items := []MemoryChunk{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
 WITH first AS (
  SELECT owner_user_id FROM memory_chunks
  WHERE embedding IS NULL AND due_at<=now() AND (embedding_lease_until IS NULL OR embedding_lease_until<=now())
  ORDER BY due_at LIMIT 1
 ), candidates AS (
  SELECT c.branch,c.record_id,c.chunk FROM memory_chunks c JOIN first f ON f.owner_user_id=c.owner_user_id
  WHERE c.embedding IS NULL AND c.due_at<=now() AND (c.embedding_lease_until IS NULL OR c.embedding_lease_until<=now())
  ORDER BY c.due_at LIMIT $1 FOR UPDATE OF c SKIP LOCKED
 ) UPDATE memory_chunks c SET embedding_attempt_id=gen_random_uuid(),embedding_lease_until=now()+interval '5 minutes',
   embedding_attempts=c.embedding_attempts+1
 FROM candidates p WHERE c.branch=p.branch AND c.record_id=p.record_id AND c.chunk=p.chunk
 RETURNING c.owner_user_id,c.space_id,c.branch,c.record_id,c.chunk,c.text,c.content_hash,c.embedding_attempt_id`, limit)
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

// CompleteMemoryChunk stores a chunk's vector unless its text changed meanwhile.
func (db *Database) CompleteMemoryChunk(ctx context.Context, chunk MemoryChunk, embedding []float64, model string) error {
	if len(embedding) != 768 || strings.TrimSpace(model) == "" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE memory_chunks SET embedding=$1::vector,embedding_model=$2,embedding_lease_until=NULL,updated_at=now()
 WHERE branch=$3 AND record_id=$4 AND chunk=$5 AND content_hash=$6 AND embedding_attempt_id=$7::uuid AND embedding IS NULL`,
			aiVectorLiteral(embedding), model, chunk.Branch, chunk.RecordID, chunk.Chunk, chunk.ContentHash, chunk.AttemptID)
		return err
	})
}

// RetryMemoryChunk backs a failed chunk off: a minute at first, a day after
// repeated failures, so a broken record never spends without end.
func (db *Database) RetryMemoryChunk(ctx context.Context, chunk MemoryChunk) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE memory_chunks SET embedding_lease_until=NULL,
 due_at=now()+CASE WHEN embedding_attempts>=$5 THEN interval '1 day' ELSE interval '1 minute' END
 WHERE branch=$1 AND record_id=$2 AND chunk=$3 AND embedding_attempt_id=$4::uuid`, chunk.Branch, chunk.RecordID, chunk.Chunk, chunk.AttemptID, memoryChunkAttempts)
		return err
	})
}
