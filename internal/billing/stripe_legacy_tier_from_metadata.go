package billing

import (
	"errors"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/stripe/stripe-go/v82"
	paymentintentapi "github.com/stripe/stripe-go/v82/paymentintent"
	subscriptionapi "github.com/stripe/stripe-go/v82/subscription"
)

func legacyTierFromMetadata(value string) (db.Tier, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "personal":
		return db.TierPro, true
	case "pro", "max":
		return db.TierPro, true
	default:
		return "", false
	}
}

func subscriptionEventFromStripe(
	subscription *stripe.Subscription,
) (*subscriptionEvent, error) {
	if subscription == nil || strings.TrimSpace(subscription.ID) == "" {
		return nil, errors.New("Stripe returned an invalid subscription")
	}
	if subscription.Customer == nil ||
		strings.TrimSpace(subscription.Customer.ID) == "" {
		return nil, errors.New("Stripe subscription has no customer")
	}
	event := &subscriptionEvent{
		ID:                subscription.ID,
		Customer:          subscription.Customer.ID,
		Status:            string(subscription.Status),
		Metadata:          subscription.Metadata,
		CancelAtPeriodEnd: subscription.CancelAtPeriodEnd,
		CanceledAt:        subscription.CanceledAt,
	}
	if subscription.Items == nil || len(subscription.Items.Data) != 1 {
		return nil, errors.New("Stripe subscription must contain exactly one item")
	}
	item := subscription.Items.Data[0]
	if item == nil || item.Price == nil {
		return nil, errors.New("Stripe subscription item has no price")
	}
	itemEvent := struct {
		CurrentPeriodEnd int64 `json:"current_period_end"`
		Price            struct {
			ID        string `json:"id"`
			Recurring struct {
				Interval string `json:"interval"`
			} `json:"recurring"`
		} `json:"price"`
	}{}
	itemEvent.CurrentPeriodEnd = item.CurrentPeriodEnd
	itemEvent.Price.ID = item.Price.ID
	if item.Price.Recurring != nil {
		itemEvent.Price.Recurring.Interval = string(item.Price.Recurring.Interval)
	}
	event.Items.Data = append(event.Items.Data, itemEvent)
	return event, nil
}

func periodEndFromSubscriptionEvent(event *subscriptionEvent) *time.Time {
	if event == nil {
		return nil
	}
	raw := event.CurrentPeriodEnd
	if raw <= 0 && len(event.Items.Data) > 0 {
		raw = event.Items.Data[0].CurrentPeriodEnd
	}
	if raw <= 0 {
		return nil
	}
	value := time.Unix(raw, 0).UTC()
	return &value
}

func fetchSubscriptionFromStripe(subscriptionID string) (*stripe.Subscription, error) {
	secretKey := strings.TrimSpace(envconfig.Getenv("STRIPE_SECRET_KEY"))
	if secretKey == "" {
		return nil, errors.New("STRIPE_SECRET_KEY is required for subscription reconciliation")
	}
	if strings.TrimSpace(subscriptionID) == "" {
		return nil, errors.New("Stripe subscription id is required")
	}
	stripe.Key = secretKey
	return subscriptionapi.Get(subscriptionID, nil)
}

func fetchChargeIDFromStripe(paymentIntentID string) (string, error) {
	secretKey := strings.TrimSpace(envconfig.Getenv("STRIPE_SECRET_KEY"))
	if secretKey == "" || paymentIntentID == "" {
		return "", nil
	}

	stripe.Key = secretKey
	params := &stripe.PaymentIntentParams{}
	params.AddExpand("latest_charge")

	intent, err := paymentintentapi.Get(paymentIntentID, params)
	if err != nil || intent == nil || intent.LatestCharge == nil {
		return "", err
	}

	return intent.LatestCharge.ID, nil
}
