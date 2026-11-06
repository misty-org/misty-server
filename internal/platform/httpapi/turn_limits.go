package api

import (
	"strings"
	"time"

	envconfig "github.com/misty-org/misty-server/internal/platform/config"
)

// maxEvalTurnLimit is the most model turns a development eval may ask for, as the
// Odysseys leaders run with 100 to 200 steps. The database and the runtime allow it too.
const maxEvalTurnLimit = 200

// evalServer reports a server started as a development stack. Unset is not one, so a
// self-hosted server never honors a request's eval settings.
func evalServer() bool {
	return strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "development")
}

// evalTurnLimit is the turn limit a development eval asks for (evals/odysseys in the app's
// repository), which long benchmark tasks need. 0 keeps the ordinary limit.
func evalTurnLimit(requested int) int {
	if requested <= 0 || !evalServer() {
		return 0
	}
	return min(requested, maxEvalTurnLimit)
}

// evalExecutionLimitMS gives a development eval that asks for its own turn limit four hours
// of execution instead of the 30-minute default: the eval stops a run when it stops making
// progress, not on a clock. 0 keeps the default.
func evalExecutionLimitMS(requestedTurns int) int64 {
	if requestedTurns <= 0 || !evalServer() {
		return 0
	}
	return int64(4 * time.Hour / time.Millisecond)
}
