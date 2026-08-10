package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/stripe/stripe-go/v82"
)

func TestReconciliationFailureUsesGraceThenExpiresLocally(t *testing.T) {
	database := openIntegrationDatabase(t)
	setSubscriptionTestConfig(t)
	user, err := database.CreateUser(
		"Grace Subscriber",
		"grace-subscriber@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	periodEnd := now.Add(-time.Hour)
	local := &db.StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_grace", StripeCustomerID: "cus_grace",
		StripePriceID: "price_max_month", Tier: db.TierMax,
		BillingInterval: "month", Status: db.SubscriptionStatusActive,
		CurrentPeriodEnd: &periodEnd, ReconcileAfter: now.Add(-time.Minute),
	}
	if err := database.UpsertStripeSubscription(local); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyEffectiveSubscriptionEntitlement(local); err != nil {
		t.Fatal(err)
	}
	service := billing.NewStripeService(
		database,
		billing.WithSubscriptionFetcher(func(string) (*stripe.Subscription, error) {
			return nil, errors.New("temporary Stripe outage")
		}),
	)
	report, err := service.ReconcileSubscriptions(context.Background(), now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || report.EntitlementsExpired != 0 {
		t.Fatalf("within-grace report = %#v", report)
	}
	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != db.TierMax {
		t.Fatalf("temporary outage revoked access inside grace: %#v", license)
	}

	oldEnd := now.Add(-73 * time.Hour)
	if _, err := database.Conn.Exec(`
		UPDATE stripe_subscriptions
		SET current_period_end=$2,reconcile_after=$3
		WHERE stripe_subscription_id=$1
	`, local.StripeSubscriptionID, oldEnd, now.Add(-time.Minute)); err != nil {
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
	if report.Failed != 1 || report.EntitlementsExpired != 1 {
		t.Fatalf("expired-grace report = %#v", report)
	}
	license, err = database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != db.TierBasic {
		t.Fatalf("indefinitely stale entitlement remained paid: %#v", license)
	}
}

func stripeSubscriptionFixture(
	user *db.User,
	status stripe.SubscriptionStatus,
	periodEnd time.Time,
	priceID string,
	tier db.Tier,
	interval string,
) *stripe.Subscription {
	return &stripe.Subscription{
		ID: "sub_reconciled", Customer: &stripe.Customer{ID: "cus_reconciled"},
		Status: status,
		Metadata: map[string]string{
			"user_id": user.ID, "license_id": user.LicenseID,
			"tier": string(tier), "interval": interval, "kind": "subscription",
		},
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodEnd: periodEnd.Unix(),
			Price: &stripe.Price{
				ID: priceID,
				Recurring: &stripe.PriceRecurring{
					Interval: stripe.PriceRecurringInterval(interval),
				},
			},
		}}},
	}
}
