package api

import (
	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type TestingBillingAIUsage = billingAIUsage

func TestingPersonalBillingAIUsage(wallet *db.HostedAIWallet) TestingBillingAIUsage {
	return personalBillingAIUsage(wallet)
}
func TestingSpaceBillingAIUsage(wallet *db.SpaceHostedAIWallet) TestingBillingAIUsage {
	return spaceBillingAIUsage(wallet)
}
func TestingMailThreadToDTO(thread mailintegration.Thread) mailThreadDTO {
	return mailThreadToDTO(thread)
}
