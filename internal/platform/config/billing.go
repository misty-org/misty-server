package config

import (
	"fmt"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"strings"
)

func BillingAdapter() (billingadapter.Adapter, error) {
	mode := strings.ToLower(strings.TrimSpace(Getenv("MISTY_DEPLOYMENT_MODE")))
	if mode != "" && mode != "hosted" && mode != "self_hosted" {
		return nil, fmt.Errorf("invalid MISTY_DEPLOYMENT_MODE")
	}
	return billingadapter.New(billingadapter.Config{Mode: Getenv("MISTY_BILLING_ADAPTER"), URL: Getenv("MISTY_BILLING_URL"), Secret: Getenv("MISTY_BILLING_SECRET"), Hosted: mode == "hosted", AllowLoopbackHTTP: strings.EqualFold(strings.TrimSpace(Getenv("MISTY_ENVIRONMENT")), "development")})
}
