package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestOnlyTrialingAndActiveSubscriptionsReceiveProEntitlements(t *testing.T) {
	for _, status := range []string{SubscriptionStatusTrialing, SubscriptionStatusActive} {
		if !SubscriptionAllowsPaidAccess(status) {
			t.Fatalf("status %q should receive Pro entitlements", status)
		}
	}
	for _, status := range []string{SubscriptionStatusPastDue, "canceled", "incomplete", "unpaid"} {
		if SubscriptionAllowsPaidAccess(status) {
			t.Fatalf("status %q should receive Free entitlements", status)
		}
	}
}
