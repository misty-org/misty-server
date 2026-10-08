package api

import (
	"context"
	"sync"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/aimodels"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Plans decide which models a run may use. Basic runs Thinking and Seeing on
// the fast model, so the free plan stretches far; paid plans default to the mid
// model and may pick any priced model, flagship included.
func fastModel() string { return envOr("MISTY_AI_FAST_MODEL", "openai/gpt-6-luna") }
func paidDefaultModel() string {
	return envOr("MISTY_AI_PAID_DEFAULT_MODEL", "openai/gpt-6.1-sol")
}

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

// planRoutes applies the plan to a run's default routes and returns the models
// the plan forces regardless of the account's or conversation's choice.
func planRoutes(ctx context.Context, database *db.Database, user string, defaults []aimodels.Route) ([]aimodels.Route, map[string]string) {
	plan := accountPlan(ctx, database, user)
	if plan == "" {
		return defaults, nil
	}
	if plan == "basic" {
		return defaults, map[string]string{"agent": fastModel(), "vision": fastModel()}
	}
	out := append([]aimodels.Route(nil), defaults...)
	for i, r := range out {
		if (r.Role == "agent" || r.Role == "vision") && r.Model == agent.FrontierDefaultModelID() {
			out[i].Model = paidDefaultModel()
		}
	}
	return out, nil
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
