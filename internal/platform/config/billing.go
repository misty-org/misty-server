package config

import (
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"strings"
)

// BillingAdapter requires the billing service in production. Development and
// tests may run without one.
func BillingAdapter() (billingadapter.Adapter, error) {
	environment := strings.TrimSpace(Getenv("MISTY_ENVIRONMENT"))
	development := strings.EqualFold(environment, "development")
	return billingadapter.New(billingadapter.Config{Mode: Getenv("MISTY_BILLING_ADAPTER"), URL: Getenv("MISTY_BILLING_URL"), Secret: Getenv("MISTY_BILLING_SECRET"), Required: strings.EqualFold(environment, "production"), AllowLoopbackHTTP: development, AllowDockerHostHTTP: development})
}
