package integration

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
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
