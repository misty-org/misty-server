package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
			Prompt   string `json:"prompt"`
			Timezone string `json:"timezone,omitempty"`
		}
		if err := decodeAIJSON(w, r, &body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		input := aiInvocationInput{
			Mode: "drawer", SurfaceID: "global", Trigger: "explicit", Prompt: strings.TrimSpace(body.Prompt),
			IdempotencyKey: "ai-complete:" + uuid.NewString(), Timezone: body.Timezone,
		}
		if err := validateAIInvocationInput(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_invocation", "message": err.Error()})
			return
		}
		if !s.agentRuntime.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "agent_runtime_unavailable", "message": "Misty's agent runtime is not configured."})
			return
		}
		payload, _ := json.Marshal(input)
		now := time.Now().UTC()
		stored, _, err := s.database.CreateAIInvocationRecord(r.Context(), db.AIInvocationRecord{
			ID: "invocation_" + uuid.NewString(), UserID: userID, SurfaceID: "global", Mode: "drawer", Trigger: "explicit",
			State: "queued", IdempotencyKey: input.IdempotencyKey, RequestPayload: payload, ExpiresAt: now.Add(aiInvocationTTL),
		})
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if _, err := s.invocations.restoreDurable(r.Context(), stored); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if _, err := s.agentRuntime.Start(r.Context(), stored.ID); err != nil {
			s.invocations.fail(stored.ID, "Misty could not start the agent runtime. Please try again.")
			TestingWriteAIError(w, err)
			return
		}
		text, _, err := s.awaitAIInvocationAnswer(r, userID, stored.ID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"text": text, "model": agent.InitialSelectedModelID})
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
