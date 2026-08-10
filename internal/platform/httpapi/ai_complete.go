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

func (s *AIService) Complete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := decodeAIJSON(w, r, &body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		release, ok := s.acquireProviderCall(w, userID)
		if !ok {
			return
		}
		defer release()
		text, usage, err := s.runtime.CompleteWithModelContext(r.Context(), userID, body.Prompt, db.CreditMeterAutomationAI, agent.InitialSelectedModelID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"text": text, "model": agent.InitialSelectedModelID, "hosted_ai_used_ratio": usage.UsedRatio, "hosted_ai_reset_at": usage.ResetAt})
	}
}

func agentDocumentsEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_DOCUMENTS_ENABLED")), "true")
}

func (s *AIService) acquireProviderCall(w http.ResponseWriter, userID string) (func(), bool) {
	release, retryAfter, allowed := s.guard.AcquireProviderCall(userID)
	if !allowed {
		TestingWriteAIRateLimit(w, retryAfter)
		return nil, false
	}
	return release, true
}

func TestingWriteAIRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := retryAfterSeconds(retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"code": "rate_limited", "message": "Agent request limit reached. Try again later.",
		"retry_after_seconds": seconds,
	})
}

func (s *AIService) agentTierForUser(userID string) (agent.AgentTier, error) {
	license, err := s.database.GetLicenseByUserID(userID)
	if err != nil {
		return agent.TierLow, err
	}
	if license == nil {
		return agent.TierLow, nil
	}
	return TestingAgentTierForLicenseTier(license.Tier), nil
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
