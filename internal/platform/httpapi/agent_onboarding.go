package api

import (
	"context"
	"log"
	"net/http"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// recordInvocationOnboarding notes the Get started steps a newly accepted request
// does: asking with a web page in front, or running a workflow. Best effort; a
// failure never fails the request.
func (s *AIService) recordInvocationOnboarding(ctx context.Context, userID string, body aiInvocationInput) {
	var steps []string
	if body.CurrentTab != nil && body.CurrentTab.URL != "" {
		steps = append(steps, db.OnboardingStepPageAsk)
	}
	// resolveInvocationMethod only lets a workflow's version through.
	if body.MethodVersionID != "" {
		steps = append(steps, db.OnboardingStepWorkflowRun)
	}
	for _, step := range steps {
		if err := s.database.RecordAgentOnboardingStep(ctx, userID, step); err != nil {
			log.Printf("record onboarding step %s: %v", step, err)
		}
	}
}

// AgentOnboarding lists the Get started steps only the server can see the account do.
func (s *AIService) AgentOnboarding() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		steps, err := s.database.AgentOnboardingSteps(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"steps": steps})
	}
}
