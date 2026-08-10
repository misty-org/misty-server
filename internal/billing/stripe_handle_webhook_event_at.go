package billing

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
)

func (service *StripeService) HandleWebhookEventAt(
	eventID, eventType string,
	eventCreatedAt time.Time,
	payload json.RawMessage,
) error {
	if eventID != "" {
		processed, err := service.database.StripeEventProcessed(eventID)
		if err != nil {
			return err
		}
		if processed {
			return nil
		}
	}
	switch eventType {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var session checkoutCompletedEvent
		if err := json.Unmarshal(payload, &session); err != nil {
			log.Println("Failed to parse checkout session:", err)
			return err
		}
		if err := service.handleCheckout(&session); err != nil {
			return err
		}
	case "checkout.session.expired":
		var session checkoutCompletedEvent
		if err := json.Unmarshal(payload, &session); err != nil {
			return err
		}
		if err := service.database.ExpireSubscriptionCheckoutBySessionID(
			context.Background(),
			session.ID,
		); err != nil {
			return err
		}
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		var subscription subscriptionEvent
		if err := json.Unmarshal(payload, &subscription); err != nil {
			return err
		}
		if err := service.handleSubscriptionChanged(
			&subscription,
			eventID,
			eventCreatedAt,
			true,
		); err != nil {
			return err
		}
	case "invoice.paid", "invoice.payment_failed":
		var invoice invoiceEvent
		if err := json.Unmarshal(payload, &invoice); err != nil {
			return err
		}
		if eventType == "invoice.paid" {
			if err := service.handleInvoicePaid(&invoice); err != nil {
				return err
			}
		}
	case "charge.refunded":
		var charge refundedChargeEvent
		if err := json.Unmarshal(payload, &charge); err != nil {
			log.Println("Failed to parse refunded charge:", err)
			return err
		}
		service.handleChargeRefunded(&charge)
	case "charge.dispute.created":
		var dispute disputeEvent
		if err := json.Unmarshal(payload, &dispute); err != nil {
			log.Println("Failed to parse dispute:", err)
			return err
		}
		service.handleChargeDisputeCreated(&dispute)
	}
	if eventID != "" {
		return service.database.MarkStripeEventProcessed(eventID, eventType)
	}
	return nil
}

func (service *StripeService) handleCheckout(session *checkoutCompletedEvent) error {
	kind := strings.TrimSpace(session.Metadata["kind"])
	if session.Mode == "subscription" || kind == "subscription" {
		if err := service.database.CompleteSubscriptionCheckoutBySessionID(
			context.Background(),
			session.ID,
		); err != nil {
			return err
		}
		if strings.TrimSpace(session.Subscription) == "" {
			// customer.subscription.* remains the normal entitlement path.
			return nil
		}
		canonical, err := service.fetchSubscription(session.Subscription)
		if err != nil {
			return err
		}
		event, err := subscriptionEventFromStripe(canonical)
		if err != nil {
			return err
		}
		return service.handleSubscriptionChanged(
			event,
			"",
			time.Time{},
			false,
		)
	}
	// One-time lifetime products and credit packs are retired. Acknowledging
	// stale events prevents Stripe retries without granting an entitlement.
	log.Printf("Ignoring retired one-time checkout event for session %s", session.ID)
	return nil
}

func (service *StripeService) handleSubscriptionChanged(
	event *subscriptionEvent,
	sourceEventID string,
	sourceEventCreatedAt time.Time,
	rejectOlder bool,
) error {
	userID := strings.TrimSpace(event.Metadata["user_id"])
	licenseID := strings.TrimSpace(event.Metadata["license_id"])
	tier, ok := TestingTierFromMetadata(event.Metadata["tier"])
	metadataInterval := BillingInterval(strings.ToLower(strings.TrimSpace(event.Metadata["interval"])))
	if userID == "" || licenseID == "" || !ok || !TestingValidPaidTier(tier) ||
		!TestingValidInterval(metadataInterval) || strings.TrimSpace(event.Metadata["kind"]) != "subscription" {
		return errors.New("subscription metadata is invalid")
	}
	user, err := service.database.GetUserByID(userID)
	if err != nil {
		return err
	}
	if user == nil || user.LicenseID != licenseID {
		return errors.New("subscription license does not match user")
	}
	if len(event.Items.Data) != 1 {
		return errors.New("subscription must contain exactly one configured price")
	}
	priceID := strings.TrimSpace(event.Items.Data[0].Price.ID)
	catalogTier, catalogInterval, catalogOK := TestingConfiguredSubscriptionPrice(priceID)
	if !catalogOK {
		return errors.New("subscription price is not in the configured catalog")
	}
	if catalogTier != tier {
		return errors.New("subscription tier metadata does not match the Stripe price")
	}
	if catalogInterval != metadataInterval {
		return errors.New("subscription interval metadata does not match the Stripe price")
	}
	if itemInterval := strings.ToLower(strings.TrimSpace(event.Items.Data[0].Price.Recurring.Interval)); itemInterval != "" && itemInterval != string(catalogInterval) {
		return errors.New("subscription recurring interval does not match the Stripe price")
	}
	tier = catalogTier
	interval := string(catalogInterval)
	var periodEnd, canceledAt *time.Time
	if event.CurrentPeriodEnd > 0 {
		value := time.Unix(event.CurrentPeriodEnd, 0).UTC()
		periodEnd = &value
	} else if len(event.Items.Data) > 0 && event.Items.Data[0].CurrentPeriodEnd > 0 {
		value := time.Unix(event.Items.Data[0].CurrentPeriodEnd, 0).UTC()
		periodEnd = &value
	}
	if event.CanceledAt > 0 {
		value := time.Unix(event.CanceledAt, 0).UTC()
		canceledAt = &value
	}
	status := strings.ToLower(strings.TrimSpace(event.Status))
	if db.SubscriptionAllowsPaidAccess(status) && periodEnd == nil {
		return errors.New("paid Stripe subscription has no billing period end")
	}
	var sourceCreated *time.Time
	if !sourceEventCreatedAt.IsZero() {
		value := sourceEventCreatedAt.UTC()
		sourceCreated = &value
	}
	now := time.Now().UTC()
	previous, err := service.database.GetStripeSubscriptionByStripeID(event.ID)
	if err != nil {
		return err
	}
	subscription := &db.StripeSubscription{UserID: userID, LicenseID: licenseID, StripeSubscriptionID: event.ID,
		StripeCustomerID: event.Customer, StripePriceID: priceID, Tier: tier, BillingInterval: interval,
		Status: status, CurrentPeriodEnd: periodEnd,
		CancelAtPeriodEnd: event.CancelAtPeriodEnd, CanceledAt: canceledAt,
		SourceEventCreatedAt: sourceCreated, SourceEventID: sourceEventID,
		ReconcileAfter: nextSubscriptionReconcileAt(now, periodEnd)}
	applied := true
	if rejectOlder {
		applied, err = service.database.UpsertStripeSubscriptionFromWebhook(subscription)
	} else {
		err = service.database.UpsertStripeSubscription(subscription)
	}
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	if err := service.database.ApplyEffectiveSubscriptionEntitlement(subscription); err != nil {
		return err
	}
	if service.analyticsEnabled(userID) {
		properties := telemetry.SubscriptionProperties{PlanID: string(tier), Status: subscription.Status, BillingInterval: telemetryInterval(interval)}
		wasActive := previous != nil && db.SubscriptionAllowsPaidAccess(previous.Status)
		isActive := db.SubscriptionAllowsPaidAccess(subscription.Status)
		if !wasActive && isActive {
			service.telemetry.SubscriptionStarted(userID, properties)
		}
		if wasActive && !isActive {
			properties.Status = "canceled"
			service.telemetry.SubscriptionCanceled(userID, properties)
		}
	}
	isActive := db.SubscriptionAllowsPaidAccess(subscription.Status)
	if isActive {
		if _, err := service.database.StartSubscriptionCreditPeriod(userID, tier, time.Now(), event.ID+":activation"); err != nil {
			return err
		}
	}
	effectiveLicense, err := service.database.GetLicenseByUserID(userID)
	if err != nil || effectiveLicense == nil {
		return err
	}
	_, err = service.database.GetOrCreateCreditWallet(userID, effectiveLicense.Tier, time.Now())
	return err
}
