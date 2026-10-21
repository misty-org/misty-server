package api

import (
	"context"
	"sync"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/aimodels"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Plans decide which models a run may use. Basic runs Thinking and Seeing on
// the fast model, so the free plan stretches far; paid plans default to the mid
// model and may pick any priced model, flagship included.
func fastModel() string { return envOr("MISTY_AI_FAST_MODEL", "openai/gpt-6-luna") }

var planTiers = struct {
	sync.Mutex
	entries map[string]planTierEntry
}{entries: map[string]planTierEntry{}}

type planTierEntry struct {
	tier    string
	expires time.Time
}

// accountPlan is the account's billing plan, cached briefly so admissions do
// not each ask billing. An empty plan means billing is off (development).
func accountPlan(ctx context.Context, database *db.Database, user string) string {
	if !database.BillingService().Adapter.Enabled() {
		return ""
	}
	planTiers.Lock()
	if e, ok := planTiers.entries[user]; ok && time.Now().Before(e.expires) {
		planTiers.Unlock()
		return e.tier
	}
	planTiers.Unlock()
	tier := "basic"
	if summary, err := accountBillingSummary(ctx, database, user); err == nil {
		if t, ok := summary["tier"].(string); ok && t != "" {
			tier = t
		}
	}
	planTiers.Lock()
	if len(planTiers.entries) > 10_000 {
		planTiers.entries = map[string]planTierEntry{}
	}
	planTiers.entries[user] = planTierEntry{tier, time.Now().Add(30 * time.Second)}
	planTiers.Unlock()
	return tier
}

// planRoutes returns a run's default routes unchanged. Every plan runs the same house
// model (agent.HouseModel); plans differ in usage limits, never in how smart the agent is.
// The second result, the models a plan forces, is always empty.
func planRoutes(ctx context.Context, database *db.Database, user string, defaults []aimodels.Route) ([]aimodels.Route, map[string]string) {
	return defaults, nil
}

// planAllowsModel reports whether the account's plan may pick a model.
func planAllowsModel(ctx context.Context, database *db.Database, user, model string) bool {
	return model == "" || accountPlan(ctx, database, user) != "basic" || model == fastModel()
}

// planTurnLimit is how many model turns one ordinary request may take. Larger
// plans run larger tasks in a single request; usage limits still bound cost.
func planTurnLimit(ctx context.Context, database *db.Database, user string) int {
	switch accountPlan(ctx, database, user) {
	case "max":
		return 30
	case "ultra":
		return 60
	case "ultra_plus":
		return 90
	}
	return 0
}

// evalTurnLimit is the plan's turn limit, or a development eval's own (at most 200) where the
// stack allows client model choice. Evals compare against benchmarks run with long step budgets
// (docs/plans/odysseys-campaign.md).
func evalTurnLimit(requested, plan int) int {
	if requested <= 0 || !serveragent.ClientModelChoice() {
		return plan
	}
	return min(requested, 200)
}

// evalExecutionLimitMS gives a development eval that asks for its own turn limit four hours
// of execution instead of the 30-minute default: the eval stops a run when it stops making
// progress, not on a clock (evals/odysseys/watchdog.ts). 0 keeps the default.
func evalExecutionLimitMS(requestedTurns int) int64 {
	if requestedTurns <= 0 || !serveragent.ClientModelChoice() {
		return 0
	}
	return int64(4 * time.Hour / time.Millisecond)
}
