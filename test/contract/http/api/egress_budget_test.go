package api

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestEgressGuardCapsBytesNotRequests(t *testing.T) {
	guard := NewEgressGuard(EgressBudget{
		PerIdentityDailyBytes: 1 << 20, GlobalDailyBytes: 1 << 30,
	})
	// Four requests is nothing; four large files is the whole quota. A request
	// count limit would not have noticed.
	const quarterMiB = 256 << 10
	for i := 0; i < 4; i++ {
		if !guard.Allow("acct:one", quarterMiB) {
			t.Fatalf("transfer %d refused while inside the quota", i)
		}
	}
	if guard.Allow("acct:one", quarterMiB) {
		t.Fatal("transfer past the daily byte quota was allowed")
	}
}

func TestEgressGuardIsolatesIdentities(t *testing.T) {
	guard := NewEgressGuard(EgressBudget{
		PerIdentityDailyBytes: 1 << 20, GlobalDailyBytes: 1 << 30,
	})
	if !guard.Allow("acct:one", 1<<20) {
		t.Fatal("first identity refused")
	}
	if guard.Allow("acct:one", 1) {
		t.Fatal("first identity should be exhausted")
	}
	// One heavy user must not deny service to everyone else.
	if !guard.Allow("acct:two", 1<<20) {
		t.Fatal("second identity was refused because of another's usage")
	}
}

func TestEgressGuardEnforcesGlobalCeiling(t *testing.T) {
	guard := NewEgressGuard(EgressBudget{
		PerIdentityDailyBytes: 1 << 30, GlobalDailyBytes: 2 << 20,
	})
	// Many accounts, each individually within quota, must still not drain the
	// deployment's total transfer allowance.
	allowed := 0
	for i := 0; i < 20; i++ {
		if guard.Allow(idFor(i), 1<<20) {
			allowed++
		}
	}
	if allowed > 2 {
		t.Fatalf("global ceiling allowed %d MiB, want 2", allowed)
	}
}

func idFor(index int) string {
	return "acct:" + string(rune('a'+index%26)) + string(rune('a'+index/26))
}

func TestEgressQuotaResetsAfterTheWindow(t *testing.T) {
	guard := NewEgressGuard(EgressBudget{
		PerIdentityDailyBytes: 1 << 20, GlobalDailyBytes: 1 << 30,
	})
	current := time.Now()
	guard.TestingNow = func() time.Time { return current }

	if !guard.Allow("acct:one", 1<<20) {
		t.Fatal("first transfer refused")
	}
	if guard.Allow("acct:one", 1) {
		t.Fatal("identity should be exhausted inside the window")
	}
	// A day later the allowance is fresh.
	current = current.Add(25 * time.Hour)
	if !guard.Allow("acct:one", 1<<20) {
		t.Fatal("quota did not reset after the window elapsed")
	}
}

func TestEgressGuardChargesUpFrontSoConcurrentTransfersCannotOvershoot(t *testing.T) {
	guard := NewEgressGuard(EgressBudget{
		PerIdentityDailyBytes: 10 << 20, GlobalDailyBytes: 1 << 30,
	})
	// Ten concurrent 1 MiB transfers exactly fill the quota; the eleventh must
	// be refused even though none has finished sending.
	for i := 0; i < 10; i++ {
		if !guard.Allow("acct:one", 1<<20) {
			t.Fatalf("concurrent transfer %d refused early", i)
		}
	}
	if guard.Allow("acct:one", 1<<20) {
		t.Fatal("quota was overshot by transfers charged only on completion")
	}
}

func TestEgressGuardStateStaysBounded(t *testing.T) {
	guard := NewEgressGuard(DefaultEgressBudget())
	guard.TestingMaxKeys = 100
	for i := 0; i < 5000; i++ {
		guard.Allow(idFor(i)+string(rune(i)), 1)
	}
	guard.TestingMu.Lock()
	tracked := len(guard.TestingPerKey)
	guard.TestingMu.Unlock()
	if tracked > 100 {
		t.Fatalf("tracked %d identities, want the cap of 100 to hold", tracked)
	}
}

func TestEgressBudgetFromEnvRejectsNonsense(t *testing.T) {
	t.Setenv("MISTY_EGRESS_MAX_BYTES_PER_IDENTITY_DAY", "-1")
	t.Setenv("MISTY_EGRESS_MAX_BYTES_PER_DAY", "nonsense")
	budget := EgressBudgetFromEnv()
	if budget != DefaultEgressBudget() {
		t.Fatalf("EgressBudgetFromEnv() = %+v, want the safe defaults", budget)
	}
}

func TestEgressBudgetHonoursConfiguredCeiling(t *testing.T) {
	t.Setenv("MISTY_EGRESS_MAX_BYTES_PER_IDENTITY_DAY", "1048576")
	if got := EgressBudgetFromEnv().PerIdentityDailyBytes; got != 1<<20 {
		t.Fatalf("PerIdentityDailyBytes = %d, want 1048576", got)
	}
}
