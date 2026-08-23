package integration

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/stripe/stripe-go/v82"
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
			return billing.CheckoutSessionResult{}, nil
		}),
	)

	_, err = service.CreateCheckoutSession(
		user.ID,
		db.TierPro,
		billing.BillingIntervalMonth,
	)
	if !errors.Is(err, billing.ErrSubscriptionExists) {
		t.Fatalf("checkout error = %v, want ErrSubscriptionExists", err)
	}
	if calls := stripeCalls.Load(); calls != 0 {
		t.Fatalf("Stripe checkout creator called %d times, want 0", calls)
	}
}

func TestPastDueSubscriberCreatesPaidCheckoutWithoutAnotherTrial(t *testing.T) {
	database := openIntegrationDatabase(t)
	setSubscriptionGuardTestConfig(t)

	user, err := database.CreateUser(
		"Past Due Subscriber",
		"past-due-subscriber-checkout@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	periodEnd := time.Now().UTC().Add(30 * 24 * time.Hour)
	existing := &db.StripeSubscription{
		UserID:               user.ID,
		LicenseID:            user.LicenseID,
		StripeSubscriptionID: "sub_past_due_checkout_guard",
		StripeCustomerID:     "cus_past_due_checkout_guard",
		StripePriceID:        "price_pro_month",
		Tier:                 db.TierPro,
		BillingInterval:      "month",
		Status:               db.SubscriptionStatusPastDue,
		CurrentPeriodEnd:     &periodEnd,
	}
	if err := database.UpsertStripeSubscription(existing); err != nil {
		t.Fatal(err)
	}

	var trialEligible bool
	var cancelCalls atomic.Int32
	service := billing.NewService(
		database,
		billing.WithCheckoutSessionCreator(func(
			_ billing.CheckoutConfig,
			_ *db.User,
			_ db.Tier,
			_ billing.BillingInterval,
			_ string,
			eligible bool,
			_ string,
			expiresAt time.Time,
		) (billing.CheckoutSessionResult, error) {
			trialEligible = eligible
			return billing.CheckoutSessionResult{
				ID:        "cs_paid_replacement",
				URL:       "https://checkout.stripe.test/paid-replacement",
				ExpiresAt: expiresAt,
			}, nil
		}),
		billing.WithSubscriptionCanceler(func(
			_ billing.CheckoutConfig,
			subscriptionID string,
		) (*stripe.Subscription, error) {
			cancelCalls.Add(1)
			return &stripe.Subscription{
				ID:     subscriptionID,
				Status: stripe.SubscriptionStatusCanceled,
			}, nil
		}),
	)

	checkoutURL, err := service.CreateCheckoutSession(
		user.ID,
		db.TierPro,
		billing.BillingIntervalMonth,
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkoutURL != "https://checkout.stripe.test/paid-replacement" {
		t.Fatalf("checkout URL = %q", checkoutURL)
	}
	if trialEligible {
		t.Fatal("past-due subscriber received another trial")
	}
	if calls := cancelCalls.Load(); calls != 1 {
		t.Fatalf("subscription cancel calls = %d, want 1", calls)
	}
	stored, err := database.GetStripeSubscriptionByStripeID(existing.StripeSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Status != string(stripe.SubscriptionStatusCanceled) {
		t.Fatalf("replaced subscription = %#v", stored)
	}
}
