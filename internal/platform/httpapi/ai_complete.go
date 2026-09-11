package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Legacy unscoped completion cannot bypass Misty's Space admission.
func (s *AIService) Complete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		writeJSON(w, http.StatusGone, map[string]string{"code": "misty_handoff_required", "message": "Start a Space-scoped Misty invocation through /ai/invocations."})
	}
}

func agentDocumentsEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_DOCUMENTS_ENABLED")), "true")
}

func TestingWriteAIRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := retryAfterSeconds(retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"code": "rate_limited", "message": "Agent request limit reached. Try again later.",
		"retry_after_seconds": seconds,
	})
}

// agentTierForLicenseTier deliberately routes every paid plan to the same tier;
// TestAutomaticRoutingIsTheSameForEveryPlan guards that. The parameter is the
// seam for per-plan routing if tiers are ever priced differently, so it stays
// even though it is currently unused.
func TestingAgentTierForLicenseTier(_ db.Tier) agent.AgentTier {
	return agent.TierMed
}

func (s *AIService) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := sessionUserID(r, s.database)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", false
	}
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

func decodeAIJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return TestingDecodeAIJSONWithLimit(w, r, dst, TestingMaxAIJSONBodyBytes)
}
