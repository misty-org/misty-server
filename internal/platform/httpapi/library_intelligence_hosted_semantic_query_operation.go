package api

import (
	"context"
	"errors"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type hostedSemanticQueryOperation struct {
	Vector      []float64
	Usage       serveragent.ModelUsage
	Reservation *db.HostedAIReservation
	SettleKey   string
	OnSettled   func()
	settled     bool
}

func beginHostedSemanticQuery(ctx context.Context, database *db.Database, analyzer *serveragent.SmartLibraryAnalyzer, userID, idempotencyKey, query string) (*hostedSemanticQueryOperation, error) {
	var usage serveragent.ModelUsage
	if database == nil || analyzer == nil {
		return nil, errors.New("semantic search is unavailable")
	}
	tier := db.TierBasic
	if license, err := database.GetLicenseByUserID(userID); err != nil {
		return nil, err
	} else if license != nil {
		tier = license.Tier
	}
	reservation, _, err := database.ReserveHostedAIUsage(userID, tier, db.HostedAIMeterSemanticQuery, idempotencyKey, appbilling.EstimateSemanticQueryCharge(), time.Now())
	if err != nil {
		return nil, err
	}
	vector, usage, err := analyzer.EmbedQuery(ctx, query)
	if err != nil {
		_ = database.ReleaseHostedAIReservation(reservation.ID)
		return nil, err
	}
	return &hostedSemanticQueryOperation{Vector: vector, Usage: usage, Reservation: reservation, SettleKey: idempotencyKey + ":settle"}, nil
}

func (operation *hostedSemanticQueryOperation) Settle(database *db.Database) error {
	if operation == nil || operation.Reservation == nil || operation.settled {
		return nil
	}
	charge := appbilling.SemanticQueryCharge(operation.Usage)
	_, err := database.SettleHostedAIReservation(operation.Reservation.ID, operation.SettleKey, db.HostedAIUsage{
		Provider: "vercel_ai_gateway", Model: serveragent.SmartLibraryEmbeddingModel, InputTokens: operation.Usage.InputTokens,
		ProviderCost: appbilling.SemanticQueryProviderCost(operation.Usage), ChargeMicrousd: charge,
	})
	if err == nil {
		operation.settled = true
		if operation.OnSettled != nil {
			operation.OnSettled()
		}
	}
	return err
}

func (operation *hostedSemanticQueryOperation) Release(database *db.Database) {
	if operation != nil && operation.Reservation != nil && !operation.settled {
		_ = database.ReleaseHostedAIReservation(operation.Reservation.ID)
	}
}
