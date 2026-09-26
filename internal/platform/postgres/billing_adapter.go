package db

import "github.com/kannachi323/misty/server/internal/billingadapter"

// BillingService defaults to an entirely local no-op for independently
// constructed services. Hosted startup always supplies a validated HTTP adapter.
func (db *Database) BillingService() *billingadapter.Service {
	if db.Billing != nil {
		return db.Billing
	}
	return &billingadapter.Service{Adapter: billingadapter.Disabled{}}
}
