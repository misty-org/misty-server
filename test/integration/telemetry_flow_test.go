package integration

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/test/testkit"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billing"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
)

type capturedTelemetry struct{ registrations, starts, cancellations int }

func (client *capturedTelemetry) UserRegistered(string, string, string) { client.registrations++ }
func (client *capturedTelemetry) SubscriptionStarted(string, telemetry.SubscriptionProperties) {
	client.starts++
}
func (client *capturedTelemetry) SubscriptionRenewed(string, telemetry.SubscriptionProperties) {}
func (client *capturedTelemetry) SubscriptionCanceled(string, telemetry.SubscriptionProperties) {
	client.cancellations++
}
func (client *capturedTelemetry) Close(context.Context) {}

func TestRegistrationTelemetryRequiresExplicitConsent(t *testing.T) {
	database := openIntegrationDatabase(t)
	client := &capturedTelemetry{}
	handler := api.RegisterWithTelemetry(database, client)

	request := httptest.NewRequest(http.MethodPost, "/register", bytes.NewBufferString(`{"name":"Telemetry User","username":"telemetry_`+strings.ReplaceAll(uuid.NewString(), "-", "")[:12]+`","email":"telemetry-`+uuid.NewString()+`@example.com","password":"password123"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || client.registrations != 0 {
		t.Fatalf("without consent status=%d registrations=%d", recorder.Code, client.registrations)
	}

	request = httptest.NewRequest(http.MethodPost, "/register", bytes.NewBufferString(`{"name":"Telemetry User","username":"telemetry_`+strings.ReplaceAll(uuid.NewString(), "-", "")[:12]+`","email":"telemetry-`+uuid.NewString()+`@example.com","password":"password123"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Misty-Analytics-Enabled", "true")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || client.registrations != 1 {
		t.Fatalf("with consent status=%d registrations=%d", recorder.Code, client.registrations)
	}
}

func TestStripeTelemetryIsConsentAwareAndDeduplicated(t *testing.T) {
	database := openIntegrationDatabase(t)
	testkit.LoadEnvironment()
	secret := strings.TrimSpace(envconfig.Getenv("STRIPE_WEBHOOK_SECRET"))
	if secret == "" {
		t.Fatal("missing STRIPE_WEBHOOK_SECRET")
	}
	client := &capturedTelemetry{}
	service := billing.NewStripeService(database,
		billing.WithChargeIDFetcher(func(paymentIntentID string) (string, error) { return "ch_" + paymentIntentID, nil }),
		billing.WithTelemetry(client),
	)
	handler := api.StripeWebhookWithService(secret, service)
	user, err := database.CreateUser("Telemetry Billing", "billing-"+uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := database.UpdateTelemetryPreferences(user.ID, true, false); err != nil {
		t.Fatalf("enable analytics: %v", err)
	}

	t.Setenv("STRIPE_PRICE_PRO_MONTHLY", "price_pro")
	event := map[string]any{
		"id": "sub_" + uuid.NewString(), "customer": "cus_" + uuid.NewString(), "status": "active",
		"current_period_end": time.Now().Add(30 * 24 * time.Hour).Unix(),
		"metadata":           map[string]string{"user_id": user.ID, "license_id": user.LicenseID, "tier": "pro", "interval": "month", "kind": "subscription"},
		"items":              map[string]any{"data": []any{map[string]any{"price": map[string]any{"id": "price_pro", "recurring": map[string]any{"interval": "month"}}}}},
	}
	for index := 0; index < 2; index++ {
		if recorder := sendSignedStripeWebhook(t, handler, secret, "customer.subscription.updated", event); recorder.Code != http.StatusOK {
			t.Fatalf("webhook %d status=%d", index, recorder.Code)
		}
	}
	if client.starts != 1 {
		t.Fatalf("subscription starts=%d, want 1", client.starts)
	}
}
