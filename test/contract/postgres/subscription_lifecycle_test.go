package db

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSubscriptionCheckoutAttemptIsSingleAndRecoverable(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser(
		"Checkout Guard",
		"checkout-guard@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	const callers = 8
	attemptIDs := make(chan string, callers)
	errors := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			attempt, _, beginErr := database.BeginSubscriptionCheckout(
				context.Background(),
				user.ID,
				user.LicenseID,
				TierPro,
				"month",
				now,
				35*time.Minute,
			)
			if beginErr != nil {
				errors <- beginErr
				return
			}
			attemptIDs <- attempt.ID
		}()
	}
	wait.Wait()
	close(attemptIDs)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	var expected string
	for attemptID := range attemptIDs {
		if expected == "" {
			expected = attemptID
		}
		if attemptID != expected {
			t.Fatalf("concurrent checkout attempts = %q and %q", expected, attemptID)
		}
	}
	if expected == "" {
		t.Fatal("no checkout attempt was returned")
	}

	expiresAt := now.Add(35 * time.Minute)
	if err := database.OpenSubscriptionCheckout(
		context.Background(),
		expected,
		"cs_checkout_guard",
		"https://checkout.stripe.test/guard",
		expiresAt,
	); err != nil {
		t.Fatal(err)
	}
	replayed, created, err := database.BeginSubscriptionCheckout(
		context.Background(),
		user.ID,
		user.LicenseID,
		TierPro,
		"month",
		now.Add(time.Minute),
		35*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created || replayed.ID != expected ||
		replayed.CheckoutURL != "https://checkout.stripe.test/guard" {
		t.Fatalf("replayed checkout = %#v, created=%v", replayed, created)
	}
	if err := database.ExpireSubscriptionCheckoutBySessionID(
		context.Background(),
		"cs_checkout_guard",
	); err != nil {
		t.Fatal(err)
	}
	replacement, created, err := database.BeginSubscriptionCheckout(
		context.Background(),
		user.ID,
		user.LicenseID,
		TierPro,
		"month",
		now.Add(time.Minute),
		35*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created || replacement.ID == expected {
		t.Fatalf("verified expiration did not release checkout: %#v", replacement)
	}
}

func TestOlderSubscriptionEventCannotRestoreOrChangeEntitlement(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser(
		"Ordered Subscriber",
		"ordered-subscriber@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	newerTime := time.Now().UTC()
	olderTime := newerTime.Add(-time.Hour)
	periodEnd := newerTime.Add(30 * 24 * time.Hour)
	newer := &StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_ordered", StripeCustomerID: "cus_ordered",
		StripePriceID: "price_max", Tier: TierMax, BillingInterval: "month",
		Status: "canceled", CurrentPeriodEnd: &periodEnd,
		SourceEventCreatedAt: &newerTime, SourceEventID: "evt_newer",
		ReconcileAfter: newerTime.Add(time.Hour),
	}
	applied, err := database.UpsertStripeSubscriptionFromWebhook(newer)
	if err != nil || !applied {
		t.Fatalf("newer subscription applied=%v error=%v", applied, err)
	}
	if err := database.ApplyEffectiveSubscriptionEntitlement(newer); err != nil {
		t.Fatal(err)
	}

	older := *newer
	older.Status = SubscriptionStatusActive
	older.SourceEventCreatedAt = &olderTime
	older.SourceEventID = "evt_older"
	applied, err = database.UpsertStripeSubscriptionFromWebhook(&older)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("older subscription event overwrote newer state")
	}
	stored, err := database.GetStripeSubscriptionByStripeID("sub_ordered")
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Status != "canceled" ||
		stored.SourceEventID != "evt_newer" {
		t.Fatalf("stored subscription = %#v", stored)
	}
	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != TierBasic {
		t.Fatalf("stale active event restored entitlement: %#v", license)
	}
}

func TestDuplicateAndAmbiguousEventsCannotRestorePaidAccess(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser(
		"Ambiguous Subscriber",
		"ambiguous-subscriber@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	eventTime := time.Now().UTC()
	periodEnd := eventTime.Add(30 * 24 * time.Hour)
	canceled := &StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_ambiguous", StripeCustomerID: "cus_ambiguous",
		StripePriceID: "price_pro", Tier: TierPro, BillingInterval: "month",
		Status: "canceled", CurrentPeriodEnd: &periodEnd,
		SourceEventCreatedAt: &eventTime, SourceEventID: "evt_canceled",
		ReconcileAfter: eventTime.Add(time.Hour),
	}
	applied, err := database.UpsertStripeSubscriptionFromWebhook(canceled)
	if err != nil || !applied {
		t.Fatalf("canceled event applied=%v error=%v", applied, err)
	}
	applied, err = database.UpsertStripeSubscriptionFromWebhook(canceled)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("duplicate event was applied twice")
	}

	ambiguousActive := *canceled
	ambiguousActive.Status = SubscriptionStatusActive
	ambiguousActive.SourceEventID = "evt_active_same_second"
	applied, err = database.UpsertStripeSubscriptionFromWebhook(&ambiguousActive)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("same-second active event restored canceled access")
	}
}

func TestCanceledSubscriptionCannotDowngradeAnotherActiveSubscription(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser(
		"Replacement Subscriber",
		"replacement-subscriber@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	periodEnd := now.Add(30 * 24 * time.Hour)
	older := &StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_replaced", StripeCustomerID: "cus_replaced",
		StripePriceID: "price_pro", Tier: TierPro, BillingInterval: "month",
		Status: "canceled", CurrentPeriodEnd: &periodEnd,
		ReconcileAfter: now.Add(time.Hour),
	}
	active := &StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_replacement", StripeCustomerID: "cus_replaced",
		StripePriceID: "price_max", Tier: TierMax, BillingInterval: "month",
		Status: SubscriptionStatusActive, CurrentPeriodEnd: &periodEnd,
		ReconcileAfter: now.Add(time.Hour),
	}
	if err := database.UpsertStripeSubscription(older); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertStripeSubscription(active); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyEffectiveSubscriptionEntitlement(active); err != nil {
		t.Fatal(err)
	}

	// A late deletion event for the older subscription must preserve the
	// replacement subscription's entitlement.
	if err := database.ApplyEffectiveSubscriptionEntitlement(older); err != nil {
		t.Fatal(err)
	}
	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != TierMax {
		t.Fatalf("late cancellation downgraded replacement license: %#v", license)
	}
	selected, err := database.GetStripeSubscriptionByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || selected.StripeSubscriptionID != active.StripeSubscriptionID {
		t.Fatalf("selected subscription = %#v, want active replacement", selected)
	}
}

func TestStaleSubscriptionEntitlementExpiresButStillBlocksNewCheckout(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser(
		"Stale Subscriber",
		"stale-subscriber@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	periodEnd := now.Add(-73 * time.Hour)
	subscription := &StripeSubscription{
		UserID: user.ID, LicenseID: user.LicenseID,
		StripeSubscriptionID: "sub_stale", StripeCustomerID: "cus_stale",
		StripePriceID: "price_max", Tier: TierMax, BillingInterval: "month",
		Status: SubscriptionStatusActive, CurrentPeriodEnd: &periodEnd,
		ReconcileAfter: now.Add(-time.Hour),
	}
	if err := database.UpsertStripeSubscription(subscription); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyEffectiveSubscriptionEntitlement(subscription); err != nil {
		t.Fatal(err)
	}
	expired, err := database.ExpireStaleSubscriptionEntitlements(
		context.Background(),
		now.Add(-72*time.Hour),
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("expired entitlements = %d, want 1", expired)
	}
	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if license == nil || license.Tier != TierBasic {
		t.Fatalf("stale license = %#v", license)
	}
	stored, err := database.GetStripeSubscriptionByUserID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Status != SubscriptionStatusActive ||
		!SubscriptionAllowsPaidAccess(stored.Status) {
		t.Fatalf("Stripe state was destroyed during local expiry: %#v", stored)
	}
}
