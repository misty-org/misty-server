package api

import (
	"context"
	"errors"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type hostedSemanticQueryOperation struct {
	Vector    []float64
	Usage     serveragent.ModelUsage
	OnSettled func()
	settled   bool
}

func beginHostedSemanticQuery(ctx context.Context, database *db.Database, analyzer *serveragent.SmartLibraryAnalyzer, userID, spaceID, idempotencyKey, query string) (*hostedSemanticQueryOperation, error) {
	var usage serveragent.ModelUsage
	if database == nil || analyzer == nil {
		return nil, errors.New("semantic search is unavailable")
	}
	analyzer = analyzer.WithBilling(database.BillingService(), userID, idempotencyKey)
	vector, usage, err := analyzer.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	return &hostedSemanticQueryOperation{Vector: vector, Usage: usage}, nil
}

// Provider usage was durably settled at the call boundary. These hooks retain
// cache/publication ordering for existing search callers without double billing.
func (operation *hostedSemanticQueryOperation) Settle(_ *db.Database) error {
	if operation != nil && !operation.settled {
		operation.settled = true
		if operation.OnSettled != nil {
			operation.OnSettled()
		}
	}
	return nil
}
func (operation *hostedSemanticQueryOperation) Release(_ *db.Database) {}
