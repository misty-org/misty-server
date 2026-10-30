package api

import (
	"context"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// fastModel is the operator's fast model when an account has not chosen one.
func fastModel() string { return envOr("MISTY_AI_FAST_MODEL", "openai/gpt-6-luna") }

// defaultTurnLimit is how many model turns one ordinary request may take.
// 0 keeps the runtime's own default. Every account gets the same limit.
const defaultTurnLimit = 0

// runRoutes is a run's routes: the defaults with the role models of user (the
// account whose models run it) in place. The second result, the models a run
// is forced onto, is always empty.
func runRoutes(ctx context.Context, _ *db.Database, user string, defaults []aimodels.Route) ([]aimodels.Route, map[string]string) {
	return accountRunRoutes(ctx, user, defaults), nil
}

// evalTurnLimit is the default turn limit, or a development eval's own (at most 200) where the
// stack allows client model choice. Evals compare against benchmarks run with long step budgets
// (docs/agents/evals/odysseys.md).
func evalTurnLimit(requested, fallback int) int {
	if requested <= 0 || !agent.ClientModelChoice() {
		return fallback
	}
	return min(requested, 200)
}

// evalExecutionLimitMS gives a development eval that asks for its own turn limit four hours
// of execution instead of the 30-minute default: the eval stops a run when it stops making
// progress, not on a clock (evals/odysseys/watchdog.ts). 0 keeps the default.
func evalExecutionLimitMS(requestedTurns int) int64 {
	if requestedTurns <= 0 || !agent.ClientModelChoice() {
		return 0
	}
	return int64(4 * time.Hour / time.Millisecond)
}
