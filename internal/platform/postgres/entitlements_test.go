package db

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func TestDisabledBillingDoesNotInventStoragePolicy(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_BILLING_ADAPTER", "none")
	limits, err := entitlementsForUserTx(context.Background(), nil, "account", time.Now())
	if err != nil || limits.PersonalStorageLimitBytes != 0 || limits.SpaceStorageLimitBytes != 0 {
		t.Fatalf("disabled billing metadata: %+v, %v", limits, err)
	}
}

func TestStorageEntitlementsUseAccountPlanAndRejectUnavailableLimits(t *testing.T) {
	var summary any
	status := http.StatusOK
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req billingadapter.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountID != "member" || req.Operation != "customer.summary" {
			t.Errorf("summary request: %+v, %v", req, err)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": true, "summary": summary})
	}))
	defer endpoint.Close()
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_BILLING_ADAPTER", "http")
	t.Setenv("MISTY_BILLING_URL", endpoint.URL)
	t.Setenv("MISTY_BILLING_SECRET", "entitlements-test-secret-000000000000")
	for _, tc := range []struct {
		plan  Tier
		bytes int64
	}{{TierBasic, 2_000_000_000}, {TierPro, 50_000_000_000}, {TierMax, 250_000_000_000}} {
		summary = map[string]any{"entitlements": PlanEntitlements{Plan: tc.plan, MaxOwnedSpaces: 3, PersonalStorageLimitBytes: tc.bytes, SpaceStorageLimitBytes: tc.bytes}}
		got, err := entitlementsForUserTx(context.Background(), nil, "member", time.Now())
		if err != nil || got.Plan != tc.plan || got.SpaceStorageLimitBytes != tc.bytes || got.PersonalStorageLimitBytes != tc.bytes {
			t.Fatalf("plan %s: %+v, %v", tc.plan, got, err)
		}
	}
	for _, tc := range []struct {
		name    string
		status  int
		summary any
	}{
		{"outage", 503, nil},
		{"missing entitlement", 200, map[string]any{}},
		{"placeholder", 200, map[string]any{"entitlements": PlanEntitlements{PersonalStorageLimitBytes: 9007199254740991, SpaceStorageLimitBytes: 9007199254740991}}},
	} {
		status, summary = tc.status, tc.summary
		got, err := entitlementsForUserTx(context.Background(), nil, "member", time.Now())
		if !errors.Is(err, billingadapter.ErrUnavailable) || got.BillingAvailable || got.SpaceStorageLimitBytes != 0 {
			t.Fatalf("%s: %+v, %v", tc.name, got, err)
		}
	}
}
