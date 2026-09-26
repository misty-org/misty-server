package app

import (
	"context"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"log"
	"time"
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
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		batch, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := service.RecoverAdmissions(batch, 20); err != nil && ctx.Err() == nil {
			log.Printf("billing admission recovery: %v", err)
		}
		if err := delivery.Flush(batch, 20); err != nil && ctx.Err() == nil {
			log.Printf("billing completion delivery: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
