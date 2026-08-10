package billing

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/billing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestTierFromMetadata(t *testing.T) {
	tests := []struct {
		raw  string
		want db.Tier
		ok   bool
	}{
		{raw: "personal", want: "", ok: false},
		{raw: " Pro ", want: db.TierPro, ok: true},
		{raw: "max", want: db.TierMax, ok: true},
		{raw: "basic", want: "", ok: false},
	}

	for _, tt := range tests {
		got, ok := TestingTierFromMetadata(tt.raw)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("tierFromMetadata(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSubscriptionStartTelemetryDeduplicatesCompletedCheckout(t *testing.T) {
	if !TestingShouldCaptureSubscriptionStart(nil) {
		t.Fatal("new checkout should be captured")
	}
	if TestingShouldCaptureSubscriptionStart(&db.StripePurchase{Status: TestingStripePurchaseStatusCompleted}) {
		t.Fatal("completed replay should not be captured")
	}
	if !TestingShouldCaptureSubscriptionStart(&db.StripePurchase{Status: TestingStripePurchaseStatusRefunded}) {
		t.Fatal("a non-completed state is not a duplicate start")
	}
}

func TestConfiguredSubscriptionPriceIsAuthoritative(t *testing.T) {
	t.Setenv("STRIPE_PRICE_PRO_MONTHLY", "price_pro_month")
	t.Setenv("STRIPE_PRICE_PRO_YEARLY", "price_pro_year")
	t.Setenv("STRIPE_PRICE_MAX_MONTHLY", "price_max_month")
	t.Setenv("STRIPE_PRICE_MAX_YEARLY", "price_max_year")
	for priceID, want := range map[string]struct {
		tier     db.Tier
		interval BillingInterval
	}{
		"price_pro_month": {db.TierPro, BillingIntervalMonth},
		"price_pro_year":  {db.TierPro, BillingIntervalYear},
		"price_max_month": {db.TierMax, BillingIntervalMonth},
		"price_max_year":  {db.TierMax, BillingIntervalYear},
	} {
		tier, interval, ok := TestingConfiguredSubscriptionPrice(priceID)
		if !ok || tier != want.tier || interval != want.interval {
			t.Fatalf("configured price %q = (%q, %q, %v)", priceID, tier, interval, ok)
		}
	}
	if _, _, ok := TestingConfiguredSubscriptionPrice("price_unknown"); ok {
		t.Fatal("unknown price was accepted")
	}
}
