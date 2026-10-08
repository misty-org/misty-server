package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func TestBillingRefusalsNameTheirLimitAndRefill(t *testing.T) {
	reset := time.Date(2026, 10, 10, 16, 10, 0, 0, time.UTC)
	for _, tc := range []struct {
		denial *billingadapter.Denial
		code   string
	}{
		{&billingadapter.Denial{Limit: "window", ResetsAt: &reset}, "usage_limit_reached"},
		{&billingadapter.Denial{Limit: "email"}, "email_unverified"},
		{&billingadapter.Denial{}, "billing_admission_denied"},
	} {
		recorder := httptest.NewRecorder()
		writeBillingError(recorder, tc.denial)
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusPaymentRequired || body["code"] != tc.code {
			t.Fatalf("%+v: %d %v", tc.denial, recorder.Code, body)
		}
		if tc.denial.Limit != "" && body["limit"] != tc.denial.Limit {
			t.Fatalf("limit missing: %v", body)
		}
		if tc.denial.ResetsAt != nil && body["resets_at"] != reset.Format(time.RFC3339) {
			t.Fatalf("refill time missing: %v", body)
		}
	}
}

func TestEmailGateCoversAIButNeverSync(t *testing.T) {
	gate := &AccountVerification{}
	// No database: an operation outside AI must return before any lookup.
	for _, operation := range []string{"sync.transfer", "storage.commit", "customer.summary"} {
		if err := gate.Gate(context.Background(), billingadapter.Request{AccountID: "a", Operation: operation}); err != nil {
			t.Fatalf("%s waited on email verification: %v", operation, err)
		}
	}
}
