package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/stripe/stripe-go/v82"
)

func setSubscriptionTestConfig(t *testing.T) {
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

func TestCheckoutRetriesReuseOneStripeSession(t *testing.T) {
	database := openIntegrationDatabase(t)
	setSubscriptionTestConfig(t)
	user, err := database.CreateUser(
		"Idempotent Checkout",
		"idempotent-checkout@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var sessionID string
	service := billing.NewService(
		database,
		billing.WithCheckoutSessionCreator(func(
			_ billing.CheckoutConfig,
			_ *db.User,
			_ db.Tier,
			_ billing.BillingInterval,
			_ string,
			_ bool,
			idempotencyKey string,
			expiresAt time.Time,
		) (billing.CheckoutSessionResult, error) {
			calls.Add(1)
			sessionID = "cs_" + idempotencyKey
			return billing.CheckoutSessionResult{
				ID: sessionID, URL: "https://checkout.stripe.test/session",
				ExpiresAt: expiresAt,
			}, nil
		}),
	)

	first, err := service.CreateCheckoutSession(
		user.ID,
		db.TierPro,
		billing.BillingIntervalMonth,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateCheckoutSession(
		user.ID,
		db.TierPro,
		billing.BillingIntervalMonth,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || calls.Load() != 1 {
		t.Fatalf("checkout URLs = %q/%q, Stripe calls = %d", first, second, calls.Load())
	}
	if err := database.CompleteSubscriptionCheckoutBySessionID(
		context.Background(),
		sessionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateCheckoutSession(
		user.ID,
		db.TierPro,
		billing.BillingIntervalMonth,
	); !errors.Is(err, billing.ErrSubscriptionExists) {
		t.Fatalf("completed checkout allowed replacement subscription: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("completed checkout made another Stripe call: %d", calls.Load())
	}
}

func TestReconciliationRepairsRenewalAndMissedCancellation(t *testing.T) {
	database := openIntegrationDatabase(t)
	setSubscriptionTestConfig(t)
	user, err := database.CreateUser(
		"Reconciled Subscriber",
		"reconciled-subscriber@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldEnd := now.Add(-time.Hour)
	local := &db.StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_reconciled", StripeCustomerID: "cus_reconciled",
		StripePriceID: "price_pro_month", Tier: db.TierPro,
		BillingInterval: "month", Status: db.SubscriptionStatusActive,
		CurrentPeriodEnd: &oldEnd, ReconcileAfter: now.Add(-time.Minute),
	}
	if err := database.UpsertStripeSubscription(local); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyEffectiveSubscriptionEntitlement(local); err != nil {
		t.Fatal(err)
	}

	status := stripe.SubscriptionStatusActive
	newEnd := now.Add(30 * 24 * time.Hour)
	fetch := func(_ string) (*stripe.Subscription, error) {
		return stripeSubscriptionFixture(
			user,
			status,
			newEnd,
			"price_pro_month",
			db.TierPro,
			"month",
		), nil
	}
	service := billing.NewStripeService(
		database,
		billing.WithSubscriptionFetcher(fetch),
	)
	report, err := service.ReconcileSubscriptions(context.Background(), now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 || report.Failed != 0 {
		t.Fatalf("renewal report = %#v", report)
	}
	stored, err := database.GetStripeSubscriptionByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.CurrentPeriodEnd == nil ||
		stored.CurrentPeriodEnd.Unix() != newEnd.Unix() {
		t.Fatalf("renewed subscription = %#v", stored)
	}

	status = stripe.SubscriptionStatusCanceled
	if _, err := database.Conn.Exec(
		`UPDATE stripe_subscriptions SET reconcile_after=$2 WHERE stripe_subscription_id=$1`,
		local.StripeSubscriptionID,
		now.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	report, err = service.ReconcileSubscriptions(
		context.Background(),
		now.Add(time.Minute),
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("cancellation report = %#v", report)
	}
	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != db.TierBasic {
		t.Fatalf("missed cancellation retained paid access: %#v", license)
	}
}

func TestCheckoutCompletionRepairsAMissingSubscriptionWebhook(t *testing.T) {
	database := openIntegrationDatabase(t)
	setSubscriptionTestConfig(t)
	user, err := database.CreateUser(
		"Checkout Recovery",
		"checkout-recovery@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	periodEnd := time.Now().UTC().Add(30 * 24 * time.Hour)
	canonical := stripeSubscriptionFixture(
		user,
		stripe.SubscriptionStatusActive,
		periodEnd,
		"price_pro_month",
		db.TierPro,
		"month",
	)
	service := billing.NewStripeService(
		database,
		billing.WithSubscriptionFetcher(func(string) (*stripe.Subscription, error) {
			return canonical, nil
		}),
	)
	payload, err := json.Marshal(map[string]any{
		"id": "cs_recovery", "mode": "subscription",
		"subscription": canonical.ID,
		"metadata": map[string]string{
			"kind": "subscription",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.HandleWebhookEventWithID(
		"evt_checkout_recovery",
		"checkout.session.completed",
		payload,
	); err != nil {
		t.Fatal(err)
	}
	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != db.TierPro {
		t.Fatalf("Checkout recovery did not grant canonical entitlement: %#v", license)
	}
	subscription, err := database.GetStripeSubscriptionByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if subscription == nil ||
		subscription.StripeSubscriptionID != canonical.ID {
		t.Fatalf("Checkout recovery did not persist subscription: %#v", subscription)
	}
}
