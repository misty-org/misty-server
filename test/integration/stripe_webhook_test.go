package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/test/testkit"

	"github.com/google/uuid"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	stripewebhook "github.com/stripe/stripe-go/v82/webhook"
)

func TestStripeWebhookRetiresOneTimeCheckoutProducts(t *testing.T) {
	database := openIntegrationDatabase(t)
	secret := requiredStripeWebhookSecret(t)
	handler := api.StripeWebhookWithService(secret, newTestStripeService(database))
	user, err := database.CreateUser("Retired Checkout", "retired-checkout@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "cs_" + uuid.NewString()
	paymentIntentID := "pi_" + uuid.NewString()
	recorder := sendSignedStripeWebhook(t, handler, secret, "checkout.session.completed", checkoutEventObject(user, db.TierMax, sessionID, paymentIntentID, "cus_test"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("retired checkout webhook = %d: %s", recorder.Code, recorder.Body.String())
	}
	license, _ := database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierBasic {
		t.Fatalf("retired checkout granted entitlement: %#v", license)
	}
	purchase, err := database.GetStripePurchaseByPaymentIntent(paymentIntentID)
	if err != nil || purchase != nil {
		t.Fatalf("retired checkout persisted purchase: %#v, %v", purchase, err)
	}
}

func TestStripeWebhookRejectsInvalidSignature(t *testing.T) {
	database := openIntegrationDatabase(t)
	secret := requiredStripeWebhookSecret(t)
	handler := api.StripeWebhookWithService(secret, newTestStripeService(database))
	user, err := database.CreateUser("Signature User", "signature@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	recorder := sendSignedStripeWebhook(t, handler, "whsec_wrong_secret", "checkout.session.completed", checkoutEventObject(user, db.TierPro, "cs_"+uuid.NewString(), "pi_"+uuid.NewString(), "cus_test"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid signature status = %d, want 400", recorder.Code)
	}
}

func TestStripeSubscriptionLifecycleAndHostedAIAllowance(t *testing.T) {
	database := openIntegrationDatabase(t)
	secret := requiredStripeWebhookSecret(t)
	handler := api.StripeWebhookWithService(secret, newTestStripeService(database))
	t.Setenv("STRIPE_PRICE_PRO_MONTHLY", "price_pro")
	user, err := database.CreateUser("Subscriber", "subscriber@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	subscriptionID := "sub_" + uuid.NewString()
	event := func(status string) map[string]any {
		return map[string]any{
			"id": subscriptionID, "customer": "cus_test", "status": status,
			"current_period_end": time.Now().Add(30 * 24 * time.Hour).Unix(),
			"metadata":           map[string]string{"user_id": user.ID, "license_id": user.LicenseID, "tier": "pro", "interval": "month", "kind": "subscription"},
			"items":              map[string]any{"data": []any{map[string]any{"price": map[string]any{"id": "price_pro", "recurring": map[string]any{"interval": "month"}}}}},
		}
	}
	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.created", event("active")); rec.Code != http.StatusOK {
		t.Fatalf("active webhook = %d: %s", rec.Code, rec.Body.String())
	}
	license, _ := database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierPro {
		t.Fatalf("active license = %#v", license)
	}
	wallet, err := database.GetOrCreateHostedAIWallet(user.ID, license.Tier, time.Now())
	if err != nil || wallet.WeeklyAllowanceMicrousd != db.ProWeeklyHostedAIAllowance {
		t.Fatalf("pro wallet = %#v, %v", wallet, err)
	}
	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.deleted", event("canceled")); rec.Code != http.StatusOK {
		t.Fatalf("canceled webhook = %d: %s", rec.Code, rec.Body.String())
	}
	license, _ = database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierBasic {
		t.Fatalf("canceled license = %#v", license)
	}
	wallet, err = database.GetOrCreateHostedAIWallet(user.ID, license.Tier, time.Now())
	if err != nil || wallet.WeeklyAllowanceMicrousd != db.FreeWeeklyHostedAIAllowance {
		t.Fatalf("free fallback wallet = %#v, %v", wallet, err)
	}
}

func TestMaxSubscriptionLifecycleAndMetadataValidation(t *testing.T) {
	database := openIntegrationDatabase(t)
	secret := requiredStripeWebhookSecret(t)
	handler := api.StripeWebhookWithService(secret, newTestStripeService(database))
	t.Setenv("STRIPE_PRICE_MAX_MONTHLY", "price_max")
	user, err := database.CreateUser("Max Subscriber", "max-subscriber@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	subscriptionID := "sub_" + uuid.NewString()
	event := func(status, metadataTier, metadataInterval string) map[string]any {
		return map[string]any{
			"id": subscriptionID, "customer": "cus_max", "status": status,
			"current_period_end": time.Now().Add(30 * 24 * time.Hour).Unix(),
			"metadata": map[string]string{
				"user_id": user.ID, "license_id": user.LicenseID, "tier": metadataTier,
				"interval": metadataInterval, "kind": "subscription",
			},
			"items": map[string]any{"data": []any{map[string]any{
				"price": map[string]any{"id": "price_max", "recurring": map[string]any{"interval": "month"}},
			}}},
		}
	}

	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.created", event("active", "pro", "month")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("tier/price mismatch status = %d, want 500", rec.Code)
	}
	license, _ := database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierBasic {
		t.Fatalf("mismatched metadata granted entitlement: %#v", license)
	}
	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.created", event("active", "max", "year")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("interval/price mismatch status = %d, want 500", rec.Code)
	}

	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.created", event("trialing", "max", "month")); rec.Code != http.StatusOK {
		t.Fatalf("trialing Max webhook = %d: %s", rec.Code, rec.Body.String())
	}
	license, _ = database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierMax || license.Status != db.LicenseStatusTrialing {
		t.Fatalf("trialing Max license = %#v", license)
	}
	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.updated", event("active", "max", "month")); rec.Code != http.StatusOK {
		t.Fatalf("active Max webhook = %d: %s", rec.Code, rec.Body.String())
	}
	license, _ = database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierMax {
		t.Fatalf("active Max license = %#v", license)
	}
	scheduledCancellation := event("active", "max", "month")
	scheduledCancellation["cancel_at_period_end"] = true
	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.updated", scheduledCancellation); rec.Code != http.StatusOK {
		t.Fatalf("scheduled Max cancellation webhook = %d: %s", rec.Code, rec.Body.String())
	}
	license, _ = database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierMax {
		t.Fatalf("scheduled cancellation removed Max early: %#v", license)
	}
	wallet, err := database.GetOrCreateHostedAIWallet(user.ID, license.Tier, time.Now())
	if err != nil || wallet.WeeklyAllowanceMicrousd != db.MaxWeeklyHostedAIAllowance {
		t.Fatalf("Max wallet = %#v, %v", wallet, err)
	}
	if rec := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.updated", event("canceled", "max", "month")); rec.Code != http.StatusOK {
		t.Fatalf("canceled Max webhook = %d: %s", rec.Code, rec.Body.String())
	}
	license, _ = database.GetLicenseByUserID(user.ID)
	if license == nil || license.Tier != db.TierBasic {
		t.Fatalf("canceled Max license = %#v", license)
	}
}

func requiredStripeWebhookSecret(t *testing.T) string {
	t.Helper()
	testkit.LoadEnvironment()
	secret := strings.TrimSpace(envconfig.Getenv("STRIPE_WEBHOOK_SECRET"))
	if secret == "" {
		t.Fatal("missing STRIPE_WEBHOOK_SECRET")
	}
	return secret
}

func checkoutEventObject(user *db.User, tier db.Tier, sessionID, paymentIntentID, customerID string) map[string]any {
	return map[string]any{
		"id": sessionID, "mode": "payment", "payment_intent": paymentIntentID,
		"customer": customerID, "amount_total": int64(4900), "currency": "USD",
		"metadata":         map[string]string{"user_id": user.ID, "license_id": user.LicenseID, "tier": string(tier)},
		"customer_details": map[string]any{"email": user.Email},
	}
}

func sendSignedStripeWebhook(t *testing.T, handler http.HandlerFunc, secret, eventType string, object any) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"id": "evt_" + uuid.NewString(), "object": "event", "api_version": "2020-08-27", "type": eventType, "data": map[string]any{"object": object}}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	signed := stripewebhook.GenerateTestSignedPayload(&stripewebhook.UnsignedPayload{Payload: body, Secret: secret})
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", bytes.NewReader(signed.Payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", signed.Header)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
