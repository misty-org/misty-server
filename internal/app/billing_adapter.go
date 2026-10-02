package app

import (
	"context"
	"errors"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func configureBilling(database *db.Database) error {

	adapter, err := envconfig.BillingAdapter()
	if err != nil {
		return err
	}
	database.Billing = &billingadapter.Service{Adapter: adapter, Store: db.BillingOutbox{Database: database}}
	return nil
}
func runBillingCompletions(ctx context.Context, server *Server) {
	service := server.Database.BillingService()
	if !service.Adapter.Enabled() {
		return
	}
	delivery := billingadapter.Reliable{Adapter: service.Adapter, Store: service.Store}
	runDatabaseQueue(ctx, server, "billing", func(ctx context.Context) (int, error) {
		batch, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		admissions, admissionErr := service.RecoverAdmissionsBatch(batch, 20)
		deliveries, deliveryErr := delivery.FlushBatch(batch, 20)
		voice, voiceErr := server.Database.RecoverVoiceUsageBatch(batch)
		// These existing APIs persist retry/lease deadlines themselves; the next
		// plan re-reads those deadlines and drains any remaining bounded batch.
		return admissions + deliveries + voice, errors.Join(admissionErr, deliveryErr, voiceErr)
	})
}
