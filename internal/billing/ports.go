package billing

import (
	"context"
	"time"
)

// EntitlementStore is the billing-owned persistence required to grant or
// expire paid capabilities.
type EntitlementStore interface {
	GrantEntitlement(context.Context, string, string, time.Time) error
	ExpireEntitlements(context.Context, string, time.Time) error
}

// Events reports billing lifecycle changes without coupling policy to a
// telemetry implementation.
type Events interface {
	SubscriptionStarted(context.Context, string, string)
	SubscriptionEnded(context.Context, string, string)
}
