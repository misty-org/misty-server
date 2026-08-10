package billing

import (
	"context"
	"encoding/json"
	"log"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
	"github.com/stripe/stripe-go/v82"
)

const (
	TestingStripePurchaseStatusCompleted = "completed"
	TestingStripePurchaseStatusRefunded  = "refunded"
	stripePurchaseStatusDisputed         = "disputed"
)

type checkoutCompletedEvent struct {
	ID              string            `json:"id"`
	Mode            string            `json:"mode"`
	Metadata        map[string]string `json:"metadata"`
	PaymentIntent   string            `json:"payment_intent"`
	Customer        string            `json:"customer"`
	Subscription    string            `json:"subscription"`
	PaymentStatus   string            `json:"payment_status"`
	AmountTotal     int64             `json:"amount_total"`
	Currency        string            `json:"currency"`
	CustomerDetails struct {
		Email string `json:"email"`
	} `json:"customer_details"`
}

type subscriptionEvent struct {
	ID                string            `json:"id"`
	Customer          string            `json:"customer"`
	Status            string            `json:"status"`
	Metadata          map[string]string `json:"metadata"`
	CurrentPeriodEnd  int64             `json:"current_period_end"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	CanceledAt        int64             `json:"canceled_at"`
	Items             struct {
		Data []struct {
			CurrentPeriodEnd int64 `json:"current_period_end"`
			Price            struct {
				ID        string `json:"id"`
				Recurring struct {
					Interval string `json:"interval"`
				} `json:"recurring"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

type invoiceEvent struct {
	ID            string `json:"id"`
	Subscription  string `json:"subscription"`
	BillingReason string `json:"billing_reason"`
	AmountPaid    int64  `json:"amount_paid"`
	Currency      string `json:"currency"`
	Parent        struct {
		SubscriptionDetails struct {
			Subscription string `json:"subscription"`
		} `json:"subscription_details"`
	} `json:"parent"`
}

type refundedChargeEvent struct {
	ID            string `json:"id"`
	PaymentIntent string `json:"payment_intent"`
}

type disputeEvent struct {
	ID     string `json:"id"`
	Charge string `json:"charge"`
}

type ChargeIDFetcher func(paymentIntentID string) (string, error)

type SubscriptionFetcher func(subscriptionID string) (*stripe.Subscription, error)

type StripeService struct {
	database          *db.Database
	fetchChargeID     ChargeIDFetcher
	fetchSubscription SubscriptionFetcher
	telemetry         telemetry.Client
}

type StripeOption func(*StripeService)

func NewStripeService(database *db.Database, opts ...StripeOption) *StripeService {
	service := &StripeService{
		database:          database,
		fetchChargeID:     fetchChargeIDFromStripe,
		fetchSubscription: fetchSubscriptionFromStripe,
		telemetry:         telemetry.NoopClient{},
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func WithTelemetry(client telemetry.Client) StripeOption {
	return func(service *StripeService) {
		if client != nil {
			service.telemetry = client
		}
	}
}

func WithChargeIDFetcher(fn ChargeIDFetcher) StripeOption {
	return func(service *StripeService) {
		if fn != nil {
			service.fetchChargeID = fn
		}
	}
}

func WithSubscriptionFetcher(fn SubscriptionFetcher) StripeOption {
	return func(service *StripeService) {
		if fn != nil {
			service.fetchSubscription = fn
		}
	}
}

type SubscriptionReconcileReport struct {
	Checked             int
	Updated             int
	Failed              int
	EntitlementsExpired int
}

func (service *StripeService) ReconcileSubscriptions(
	ctx context.Context,
	now time.Time,
	limit int,
) (SubscriptionReconcileReport, error) {
	var report SubscriptionReconcileReport
	subscriptions, err := service.database.ListStripeSubscriptionsDueForReconciliation(
		ctx,
		now,
		limit,
	)
	if err != nil {
		return report, err
	}
	for _, local := range subscriptions {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Checked++
		canonical, fetchErr := service.fetchSubscription(local.StripeSubscriptionID)
		if fetchErr != nil || canonical == nil {
			report.Failed++
			failure := "Stripe returned an empty subscription"
			if fetchErr != nil {
				failure = fetchErr.Error()
			}
			retryAt := now.Add(subscriptionReconcileBackoff(local.ReconcileFailures))
			if markErr := service.database.MarkStripeSubscriptionReconcileFailed(
				ctx,
				local.StripeSubscriptionID,
				failure,
				retryAt,
			); markErr != nil {
				return report, markErr
			}
			continue
		}
		event, conversionErr := subscriptionEventFromStripe(canonical)
		if conversionErr != nil {
			report.Failed++
			retryAt := now.Add(subscriptionReconcileBackoff(local.ReconcileFailures))
			if markErr := service.database.MarkStripeSubscriptionReconcileFailed(
				ctx,
				local.StripeSubscriptionID,
				conversionErr.Error(),
				retryAt,
			); markErr != nil {
				return report, markErr
			}
			continue
		}
		if applyErr := service.handleSubscriptionChanged(
			event,
			"",
			time.Time{},
			false,
		); applyErr != nil {
			report.Failed++
			retryAt := now.Add(subscriptionReconcileBackoff(local.ReconcileFailures))
			if markErr := service.database.MarkStripeSubscriptionReconcileFailed(
				ctx,
				local.StripeSubscriptionID,
				applyErr.Error(),
				retryAt,
			); markErr != nil {
				return report, markErr
			}
			continue
		}
		reconcileAt := nextSubscriptionReconcileAt(now, periodEndFromSubscriptionEvent(event))
		if markErr := service.database.MarkStripeSubscriptionReconciled(
			ctx,
			local.StripeSubscriptionID,
			now,
			reconcileAt,
		); markErr != nil {
			return report, markErr
		}
		report.Updated++
	}
	expired, err := service.database.ExpireStaleSubscriptionEntitlements(
		ctx,
		now.Add(-72*time.Hour),
		limit,
	)
	if err != nil {
		return report, err
	}
	report.EntitlementsExpired = expired
	return report, nil
}

func subscriptionReconcileBackoff(priorFailures int) time.Duration {
	if priorFailures < 0 {
		priorFailures = 0
	}
	if priorFailures > 5 {
		priorFailures = 5
	}
	return 15 * time.Minute * time.Duration(1<<priorFailures)
}

func (service *StripeService) HandleWebhookEvent(eventType string, payload json.RawMessage) {
	if err := service.HandleWebhookEventWithID("", eventType, payload); err != nil {
		log.Printf("Stripe webhook %s failed: %v", eventType, err)
	}
}

func (service *StripeService) HandleWebhookEventWithID(eventID, eventType string, payload json.RawMessage) error {
	return service.HandleWebhookEventAt(eventID, eventType, time.Time{}, payload)
}
