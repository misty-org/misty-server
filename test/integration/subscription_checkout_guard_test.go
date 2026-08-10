package integration

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func setSubscriptionGuardTestConfig(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"STRIPE_SECRET_KEY":           "sk_test",
		"STRIPE_CHECKOUT_SUCCESS_URL": "https://app.test/success",
		"STRIPE_CHECKOUT_CANCEL_URL":  "https://app.test/cancel",
		"STRIPE_PORTAL_RETURN_URL":    "https://app.test/account",
		"STRIPE_PRICE_PRO_MONTHLY":    "price_pro_month",
		"STRIPE_PRICE_PRO_YEARLY":     "price_pro_year",
		"STRIPE_PRICE_MAX_MONTHLY":    "price_max_month",
		"STRIPE_PRICE_MAX_YEARLY":     "price_max_year",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestActiveSubscriberCannotCreateAnotherCheckoutSession(t *testing.T) {
	database := openIntegrationDatabase(t)
	setSubscriptionGuardTestConfig(t)

	user, err := database.CreateUser(
		"Active Subscriber",
		"active-subscriber-checkout@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}

	periodEnd := time.Now().UTC().Add(30 * 24 * time.Hour)
	subscription := &db.StripeSubscription{
		UserID:               user.ID,
		LicenseID:            user.LicenseID,
		StripeSubscriptionID: "sub_active_checkout_guard",
		StripeCustomerID:     "cus_active_checkout_guard",
		StripePriceID:        "price_pro_month",
		Tier:                 db.TierPro,
		BillingInterval:      "month",
		Status:               db.SubscriptionStatusActive,
		CurrentPeriodEnd:     &periodEnd,
	}
	if err := database.UpsertStripeSubscription(subscription); err != nil {
		t.Fatal(err)
	}

	var stripeCalls atomic.Int32
	service := billing.NewService(
		database,
		billing.WithCheckoutSessionCreator(func(
			_ billing.CheckoutConfig,
			_ *db.User,
			_ db.Tier,
			_ billing.BillingInterval,
			_ string,
			_ bool,
			_ string,
			_ time.Time,
		) (billing.CheckoutSessionResult, error) {
			stripeCalls.Add(1)
			return billing.CheckoutSessionResult{
				ID:  "cs_should_not_exist",
				URL: "https://checkout.stripe.test/should-not-exist",
			}, nil
		}),
	)

	_, err = service.CreateCheckoutSession(
		user.ID,
		db.TierPro,
		billing.BillingIntervalMonth,
	)
	if !errors.Is(err, billing.ErrSubscriptionExists) {
		t.Fatalf("same-tier checkout error = %v, want ErrSubscriptionExists", err)
	}
	if calls := stripeCalls.Load(); calls != 0 {
		t.Fatalf("Stripe checkout creator called %d times, want 0", calls)
	}
}
