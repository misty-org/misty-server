package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestCanonicalPlanEntitlements(t *testing.T) {
	tests := []struct {
		tier           Tier
		storage        int64
		spaces         int
		unlimited      bool
		agentAllowance int64
	}{
		{TierBasic, 2_000_000_000, 3, false, BasicWeeklyAgentAllowance},
		{TierPro, 50_000_000_000, 10, false, ProWeeklyAgentAllowance},
		{TierMax, 250_000_000_000, 0, true, MaxWeeklyAgentAllowance},
	}
	for _, testCase := range tests {
		entitlements := EntitlementsForTier(testCase.tier)
		if entitlements.Plan != testCase.tier ||
			entitlements.StorageLimitBytes != testCase.storage ||
			entitlements.SpaceLimit != testCase.spaces ||
			entitlements.UnlimitedSpaces != testCase.unlimited ||
			entitlements.WeeklyHostedAIAllowance != testCase.agentAllowance {
			t.Fatalf("%s entitlements = %#v", testCase.tier, entitlements)
		}
	}
}

func TestWeeklyAgentAllowanceRatiosAreExact(t *testing.T) {
	if ProWeeklyAgentAllowance != BasicWeeklyAgentAllowance*6 {
		t.Fatalf("Pro allowance = %d, want 6 × Basic", ProWeeklyAgentAllowance)
	}
	if MaxWeeklyAgentAllowance != ProWeeklyAgentAllowance*2 {
		t.Fatalf("Max allowance = %d, want 2 × Pro", MaxWeeklyAgentAllowance)
	}
	if MaxWeeklyAgentAllowance != BasicWeeklyAgentAllowance*12 {
		t.Fatalf("Max allowance = %d, want 12 × Basic", MaxWeeklyAgentAllowance)
	}
}

func TestNormalizePlanPreservesFinalPlansAndMapsHistoricalPersonalToPro(t *testing.T) {
	if NormalizePlan(TierBasic) != TierBasic || NormalizePlan(TierPro) != TierPro || NormalizePlan(TierMax) != TierMax {
		t.Fatal("final plans were not preserved")
	}
	if NormalizePlan(TierPersonal) != TierPro {
		t.Fatal("historical Personal plan should retain Pro-equivalent entitlement")
	}
}
