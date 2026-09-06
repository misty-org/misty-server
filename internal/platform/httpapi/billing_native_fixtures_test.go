package api

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestNativeBillingAIUsageFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/migration/fixtures/billing-ai-usage.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name      string
		Allowance int64
		Balance   int64
		Reserved  int64
		Expected  billingAIUsage
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			personal := personalBillingAIUsage(&db.HostedAIWallet{WeeklyAllowanceMicrousd: f.Allowance, WeeklyRemainingMicrousd: f.Balance, ReservedMicrousd: f.Reserved, ResetAt: f.Expected.ResetAt})
			space := spaceBillingAIUsage(&db.SpaceHostedAIWallet{WeeklyAllowanceMicrousd: f.Allowance, WeeklyRemainingMicrousd: f.Balance, ReservedMicrousd: f.Reserved, ResetAt: f.Expected.ResetAt})
			if !reflect.DeepEqual(personal, f.Expected) || !reflect.DeepEqual(space, f.Expected) {
				t.Fatalf("personal=%+v space=%+v want=%+v", personal, space, f.Expected)
			}
		})
	}
}
