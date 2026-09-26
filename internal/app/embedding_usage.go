package app

import (
	"context"
	"fmt"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func embedRetrievalChunk(ctx context.Context, server *Server, chunk db.AIEmbeddingChunk) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if chunk.AccountID == "" || chunk.AttemptID == "" {
		return billingadapter.ErrInvalid
	}
	service := server.Database.BillingService()
	key := "retrieval-embedding:" + chunk.AttemptID
	analyzer := server.AIAnalyzer.WithBilling(service, chunk.AccountID, key)
	vectors, _, err := analyzer.Embed(ctx, []string{chunk.Content})
	if err != nil {
		return err
	}
	if len(vectors) != 1 {
		return fmt.Errorf("embedding provider returned an unexpected result count")
	}
	return server.Database.CompleteAIEmbeddingChunk(ctx, chunk, vectors[0], agent.SmartLibraryEmbeddingModel)
}
