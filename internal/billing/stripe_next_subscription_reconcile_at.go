package billing

import (
	"log"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
)

func nextSubscriptionReconcileAt(now time.Time, periodEnd *time.Time) time.Time {
	next := now.UTC().Add(6 * time.Hour)
	if periodEnd == nil {
		return next
	}
	nearPeriodEnd := periodEnd.UTC().Add(15 * time.Minute)
	if nearPeriodEnd.Before(now) {
		nearPeriodEnd = now.UTC().Add(15 * time.Minute)
	}
	if nearPeriodEnd.Before(next) {
		return nearPeriodEnd
	}
	return next
}

func (service *StripeService) handleInvoicePaid(invoice *invoiceEvent) error {
	subscriptionID := strings.TrimSpace(invoice.Subscription)
	if subscriptionID == "" {
		subscriptionID = strings.TrimSpace(invoice.Parent.SubscriptionDetails.Subscription)
	}
	if subscriptionID == "" {
		return nil
	}
	subscription, err := service.database.GetStripeSubscriptionByStripeID(subscriptionID)
	if err != nil || subscription == nil {
		return err
	}
	if _, err := service.database.GetOrCreateCreditWallet(subscription.UserID, subscription.Tier, time.Now()); err != nil {
		return err
	}
	if invoice.BillingReason == "subscription_cycle" && service.analyticsEnabled(subscription.UserID) {
		service.telemetry.SubscriptionRenewed(subscription.UserID, telemetry.SubscriptionProperties{
			PlanID: string(subscription.Tier), Currency: strings.ToLower(invoice.Currency), AmountMinor: invoice.AmountPaid,
			Status: subscription.Status, BillingInterval: telemetryInterval(subscription.BillingInterval),
		})
	}
	return nil
}

func telemetryInterval(interval string) string {
	if interval == "year" {
		return "yearly"
	}
	if interval == "month" {
		return "monthly"
	}
	return interval
}

func TestingConfiguredSubscriptionPrice(priceID string) (db.Tier, BillingInterval, bool) {
	var matched *TestingPriceKey
	for _, definition := range subscriptionPriceDefinitions {
		if configured := strings.TrimSpace(envconfig.Getenv(definition.env)); configured != "" && configured == strings.TrimSpace(priceID) {
			if matched != nil {
				return "", "", false
			}
			key := definition.key
			matched = &key
		}
	}
	if matched == nil {
		return "", "", false
	}
	return matched.TestingTier, matched.TestingInterval, true
}

func (service *StripeService) handleCheckoutCompleted(session *checkoutCompletedEvent) {
	if session.Mode != "payment" {
		return
	}

	userID := strings.TrimSpace(session.Metadata["user_id"])
	licenseID := strings.TrimSpace(session.Metadata["license_id"])
	tier, ok := legacyTierFromMetadata(session.Metadata["tier"])
	if userID == "" || licenseID == "" || !ok {
		log.Printf("Stripe checkout missing required metadata for session %s", session.ID)
		return
	}

	user, err := service.database.GetUserByID(userID)
	if err != nil || user == nil {
		log.Printf("Stripe checkout: user %s not found: %v", userID, err)
		return
	}
	if user.LicenseID != licenseID {
		log.Printf("Stripe checkout license mismatch for user %s: have %s want %s", userID, user.LicenseID, licenseID)
		return
	}

	if service.isReplayedCompletedCheckoutAfterReversal(session) {
		log.Printf("Ignoring replayed checkout completion for reversed session %s", session.ID)
		return
	}
	existingPurchase, _ := service.database.GetStripePurchaseByCheckoutSessionID(strings.TrimSpace(session.ID))
	alreadyCaptured := !TestingShouldCaptureSubscriptionStart(existingPurchase)

	if err := service.database.SetLicenseStateByID(licenseID, tier, db.LicenseStatusActive, nil); err != nil {
		log.Printf("Failed to activate %s license %s: %v", tier, licenseID, err)
		return
	}
	if err := service.database.SetLegacyTierByID(licenseID, &tier); err != nil {
		log.Printf("Failed to persist lifetime tier for license %s: %v", licenseID, err)
		return
	}

	chargeID, err := service.fetchChargeID(session.PaymentIntent)
	if err != nil {
		log.Printf("Failed to fetch charge for payment intent %s: %v", session.PaymentIntent, err)
	}

	if err := service.database.UpsertStripePurchase(&db.StripePurchase{
		UserID:                  userID,
		LicenseID:               licenseID,
		TierPurchased:           tier,
		StripeCheckoutSessionID: session.ID,
		StripePaymentIntentID:   session.PaymentIntent,
		StripeCustomerID:        session.Customer,
		StripeChargeID:          chargeID,
		Amount:                  session.AmountTotal,
		Currency:                strings.ToLower(strings.TrimSpace(session.Currency)),
		Status:                  TestingStripePurchaseStatusCompleted,
		EventSource:             "checkout.session.completed",
	}); err != nil {
		log.Printf("Failed to persist Stripe purchase for session %s: %v", session.ID, err)
		return
	}
	if !alreadyCaptured && service.analyticsEnabled(userID) {
		service.telemetry.SubscriptionStarted(userID, telemetry.SubscriptionProperties{
			PlanID: string(tier), Currency: strings.ToLower(strings.TrimSpace(session.Currency)), AmountMinor: session.AmountTotal,
			Status: "active", BillingInterval: "lifetime",
		})
	}

	log.Printf("Provisioned %s license for user %s", tier, userID)
}

func TestingShouldCaptureSubscriptionStart(existing *db.StripePurchase) bool {
	return existing == nil || existing.Status != TestingStripePurchaseStatusCompleted
}

func (service *StripeService) isReplayedCompletedCheckoutAfterReversal(session *checkoutCompletedEvent) bool {
	purchase, err := service.database.GetStripePurchaseByCheckoutSessionID(strings.TrimSpace(session.ID))
	if err != nil {
		log.Printf("Failed to check existing purchase for session %s: %v", session.ID, err)
		return false
	}
	if purchase == nil {
		purchase, err = service.database.GetStripePurchaseByPaymentIntent(strings.TrimSpace(session.PaymentIntent))
		if err != nil {
			log.Printf("Failed to check existing purchase for payment intent %s: %v", session.PaymentIntent, err)
			return false
		}
	}
	return purchase != nil && (purchase.Status == TestingStripePurchaseStatusRefunded || purchase.Status == stripePurchaseStatusDisputed)
}

func (service *StripeService) handleChargeRefunded(charge *refundedChargeEvent) {
	purchase, err := service.database.GetStripePurchaseByPaymentIntent(strings.TrimSpace(charge.PaymentIntent))
	if err != nil {
		log.Printf("Failed to find purchase for refunded payment intent %s: %v", charge.PaymentIntent, err)
		return
	}
	if purchase == nil {
		purchase, err = service.database.GetStripePurchaseByChargeID(strings.TrimSpace(charge.ID))
		if err != nil {
			log.Printf("Failed to find purchase for refunded charge %s: %v", charge.ID, err)
			return
		}
	}
	if purchase == nil {
		log.Printf("No purchase found for refunded charge %s", charge.ID)
		return
	}
	if purchase.Status == TestingStripePurchaseStatusRefunded {
		return
	}

	if err := service.database.UpdateStripePurchaseStatus(purchase.ID, TestingStripePurchaseStatusRefunded, "charge.refunded"); err != nil {
		log.Printf("Failed to mark purchase %s refunded: %v", purchase.ID, err)
		return
	}
	if err := service.database.SetLicenseStateByID(purchase.LicenseID, db.TierBasic, db.LicenseStatusActive, nil); err != nil {
		log.Printf("Failed to downgrade license %s after refund: %v", purchase.LicenseID, err)
	}
	_ = service.database.SetLegacyTierByID(purchase.LicenseID, nil)
	if service.analyticsEnabled(purchase.UserID) {
		service.telemetry.SubscriptionCanceled(purchase.UserID, telemetry.SubscriptionProperties{PlanID: string(purchase.TierPurchased), Currency: purchase.Currency, AmountMinor: purchase.Amount, Status: "canceled", BillingInterval: "lifetime"})
	}
}

func (service *StripeService) handleChargeDisputeCreated(dispute *disputeEvent) {
	purchase, err := service.database.GetStripePurchaseByChargeID(strings.TrimSpace(dispute.Charge))
	if err != nil {
		log.Printf("Failed to find purchase for disputed charge %s: %v", dispute.Charge, err)
		return
	}
	if purchase == nil {
		log.Printf("No purchase found for disputed charge %s", dispute.Charge)
		return
	}
	if purchase.Status == stripePurchaseStatusDisputed {
		return
	}

	if err := service.database.UpdateStripePurchaseStatus(purchase.ID, stripePurchaseStatusDisputed, "charge.dispute.created"); err != nil {
		log.Printf("Failed to mark purchase %s disputed: %v", purchase.ID, err)
		return
	}
	if err := service.database.SetLicenseStateByID(purchase.LicenseID, db.TierBasic, db.LicenseStatusActive, nil); err != nil {
		log.Printf("Failed to downgrade license %s after dispute: %v", purchase.LicenseID, err)
	}
	_ = service.database.SetLegacyTierByID(purchase.LicenseID, nil)
	if service.analyticsEnabled(purchase.UserID) {
		service.telemetry.SubscriptionCanceled(purchase.UserID, telemetry.SubscriptionProperties{PlanID: string(purchase.TierPurchased), Currency: purchase.Currency, AmountMinor: purchase.Amount, Status: "canceled", BillingInterval: "lifetime"})
	}
}

func (service *StripeService) analyticsEnabled(userID string) bool {
	settings, err := service.database.GetUserSettingsByID(userID)
	return err == nil && settings != nil && settings.AnalyticsEnabled
}

func TestingTierFromMetadata(value string) (db.Tier, bool) {
	switch db.Tier(strings.ToLower(strings.TrimSpace(value))) {
	case db.TierPro:
		return db.TierPro, true
	case db.TierMax:
		return db.TierMax, true
	default:
		return "", false
	}
}
