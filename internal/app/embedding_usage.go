package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

var errInvalidEmbeddingChunk = errors.New("invalid embedding chunk")

func embedRetrievalChunk(ctx context.Context, server *Server, chunk db.AIEmbeddingChunk) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if chunk.AccountID == "" || chunk.AttemptID == "" {
		return errInvalidEmbeddingChunk
	}
	analyzer := server.AIAnalyzer.WithAIAccount(chunk.AccountID)
	model, err := analyzer.AccountEmbeddingModel(ctx)
	if err != nil {
		return err
	}
	vectors, _, err := analyzer.Embed(ctx, []string{chunk.Content})
	if err != nil {
		return err
	}
	if len(vectors) != 1 {
		return fmt.Errorf("embedding provider returned an unexpected result count")
	}
	return server.Database.CompleteAIEmbeddingChunk(ctx, chunk, vectors[0], model)
}

// embedMemoryChunks embeds one owner's batch of memory chunks in one call on
// that owner's embedding model.
func embedMemoryChunks(ctx context.Context, server *Server, chunks []db.MemoryChunk) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	owner := chunks[0].OwnerUserID
	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		if chunk.OwnerUserID != owner || chunk.AttemptID == "" {
			return errInvalidEmbeddingChunk
		}
		texts[i] = chunk.Text
	}
	analyzer := server.AIAnalyzer.WithAIAccount(owner)
	model, err := analyzer.AccountEmbeddingModel(ctx)
	if err != nil {
		return err
	}
	vectors, _, err := analyzer.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vectors) != len(chunks) {
		return fmt.Errorf("embedding provider returned an unexpected result count")
	}
	for i, chunk := range chunks {
		if err := server.Database.CompleteMemoryChunk(ctx, chunk, vectors[i], model); err != nil {
			return err
		}
	}
	return nil
}
