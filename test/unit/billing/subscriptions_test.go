package billing

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/billing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func setStripeConfig(t *testing.T) {
	t.Helper()
	values := map[string]string{"STRIPE_SECRET_KEY": "sk_test", "STRIPE_CHECKOUT_SUCCESS_URL": "https://app/success", "STRIPE_CHECKOUT_CANCEL_URL": "https://app/cancel",
		"STRIPE_PORTAL_RETURN_URL": "https://app/account", "STRIPE_PRICE_PRO_MONTHLY": "price_pm", "STRIPE_PRICE_PRO_YEARLY": "price_py",
		"STRIPE_PRICE_MAX_MONTHLY": "price_mm", "STRIPE_PRICE_MAX_YEARLY": "price_my"}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestLoadStripeCheckoutConfig(t *testing.T) {
	setStripeConfig(t)
	cfg, err := TestingLoadStripeCheckoutConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.TestingPrices[TestingPriceKey{TestingTier: db.TierPro, TestingInterval: BillingIntervalYear}]; got != "price_py" {
		t.Fatalf("pro yearly price = %q", got)
	}
	if got := cfg.TestingPrices[TestingPriceKey{TestingTier: db.TierMax, TestingInterval: BillingIntervalMonth}]; got != "price_mm" {
		t.Fatalf("max monthly price = %q", got)
	}
}

func TestPricingConstants(t *testing.T) {
	if !TestingValidPaidTier(db.TierPro) || !TestingValidPaidTier(db.TierMax) || TestingValidPaidTier(db.TierBasic) {
		t.Fatal("paid tier validation mismatch")
	}
	if !TestingValidInterval(BillingIntervalMonth) || !TestingValidInterval(BillingIntervalYear) || TestingValidInterval("week") || TestingValidInterval("") {
		t.Fatal("billing interval validation mismatch")
	}
	if ProTrialDuration != 14*24*time.Hour {
		t.Fatal("trial duration mismatch")
	}
}

func TestStripeCheckoutConfigRequiresAllFourUniquePrices(t *testing.T) {
	setStripeConfig(t)
	t.Setenv("STRIPE_PRICE_MAX_YEARLY", "")
	if _, err := TestingLoadStripeCheckoutConfig(); err == nil || err.Error() != "STRIPE_PRICE_MAX_YEARLY is required" {
		t.Fatalf("missing Max yearly price error = %v", err)
	}
	setStripeConfig(t)
	t.Setenv("STRIPE_PRICE_MAX_YEARLY", "price_pm")
	if _, err := TestingLoadStripeCheckoutConfig(); err == nil {
		t.Fatal("duplicate Stripe Price ID was accepted")
	}
}

func TestCheckoutParamsSelectCatalogPriceAndSetMetadata(t *testing.T) {
	setStripeConfig(t)
	cfg, err := TestingLoadStripeCheckoutConfig()
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{ID: "user_test", LicenseID: "license_test", Email: "trial@example.com"}
	for _, testCase := range []struct {
		tier     db.Tier
		interval BillingInterval
		price    string
	}{
		{db.TierPro, BillingIntervalMonth, "price_pm"},
		{db.TierPro, BillingIntervalYear, "price_py"},
		{db.TierMax, BillingIntervalMonth, "price_mm"},
		{db.TierMax, BillingIntervalYear, "price_my"},
	} {
		params := TestingStripeCheckoutSessionParams(cfg, user, testCase.tier, testCase.interval, "", false)
		if len(params.LineItems) != 1 || params.LineItems[0].Price == nil || *params.LineItems[0].Price != testCase.price {
			t.Fatalf("%s/%s price params = %#v", testCase.tier, testCase.interval, params.LineItems)
		}
		for key, want := range map[string]string{
			"user_id": user.ID, "license_id": user.LicenseID, "tier": string(testCase.tier),
			"interval": string(testCase.interval), "kind": "subscription",
		} {
			if got := params.Metadata[key]; got != want || params.SubscriptionData.Metadata[key] != want {
				t.Fatalf("%s/%s metadata %q = %q/%q, want %q", testCase.tier, testCase.interval, key, got, params.SubscriptionData.Metadata[key], want)
			}
		}
		if params.ClientReferenceID == nil || *params.ClientReferenceID != user.ID {
			t.Fatalf("%s/%s client reference = %#v", testCase.tier, testCase.interval, params.ClientReferenceID)
		}
	}
}

func TestOnlyEligibleProCheckoutReceivesTrial(t *testing.T) {
	setStripeConfig(t)
	cfg, err := TestingLoadStripeCheckoutConfig()
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{ID: "user_test", LicenseID: "license_test", Email: "trial@example.com"}
	pro := TestingStripeCheckoutSessionParams(cfg, user, db.TierPro, BillingIntervalMonth, "", true)
	if pro.SubscriptionData == nil || pro.SubscriptionData.TrialPeriodDays == nil || *pro.SubscriptionData.TrialPeriodDays != 14 {
		t.Fatalf("Pro trial params = %#v", pro.SubscriptionData)
	}
	if pro.PaymentMethodCollection == nil || *pro.PaymentMethodCollection != "always" {
		t.Fatalf("Pro payment method collection = %#v", pro.PaymentMethodCollection)
	}
	max := TestingStripeCheckoutSessionParams(cfg, user, db.TierMax, BillingIntervalMonth, "", true)
	if max.SubscriptionData.TrialPeriodDays != nil || max.PaymentMethodCollection != nil {
		t.Fatalf("Max checkout incorrectly received a trial: %#v", max)
	}
	returning := TestingStripeCheckoutSessionParams(cfg, user, db.TierPro, BillingIntervalMonth, "cus_existing", false)
	if returning.SubscriptionData.TrialPeriodDays != nil || returning.PaymentMethodCollection != nil {
		t.Fatalf("returning checkout incorrectly received a trial: %#v", returning)
	}
	if returning.Customer == nil || *returning.Customer != "cus_existing" {
		t.Fatalf("returning customer = %#v", returning.Customer)
	}
}
