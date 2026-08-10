package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAIHandlersRequireAuthentication(t *testing.T) {
	service := NewAIService(&db.Database{}, agent.NewService(nil, nil))
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{name: "status", handler: service.Status(), method: http.MethodGet, path: "/ai/status"},
		{name: "complete", handler: service.Complete(), method: http.MethodPost, path: "/ai/complete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			tt.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s status = %d, want %d", tt.name, rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestWriteAIRateLimitIsStructuredAndDoesNotSuggestAutomaticRetry(t *testing.T) {
	recorder := httptest.NewRecorder()
	TestingWriteAIRateLimit(recorder, 30*time.Second)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "30" {
		t.Fatalf("status=%d retry-after=%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "rate_limited" || payload["retry_after_seconds"] != float64(30) {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestWriteAIErrorReturnsCanceledWithoutProviderDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	TestingWriteAIError(recorder, context.Canceled)
	if recorder.Code != 499 || !strings.Contains(recorder.Body.String(), `"code":"request_canceled"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteAIErrorReturnsStructuredHostedAILimit(t *testing.T) {
	recorder := httptest.NewRecorder()
	TestingWriteAIError(recorder, agent.CreditsExhaustedError{Required: 25, Available: 10})
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "hosted_ai_limit_reached" || payload["message"] == "" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAutomaticRoutingIsTheSameForEveryPlan(t *testing.T) {
	tests := map[db.Tier]agent.AgentTier{
		db.TierBasic: agent.TierMed,
		db.TierPro:   agent.TierMed,
		db.TierMax:   agent.TierMed,
	}
	for subscription, want := range tests {
		if got := TestingAgentTierForLicenseTier(subscription); got != want {
			t.Fatalf("agentTierForLicenseTier(%q) = %q, want %q", subscription, got, want)
		}
	}
}
