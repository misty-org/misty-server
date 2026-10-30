package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func writeAIProviderError(w http.ResponseWriter, err error) {
	if writeModelError(w, err) {
		return
	}
	status, message := http.StatusInternalServerError, "Could not save the model choice. Try again."
	switch {
	case errors.Is(err, db.ErrSpaceInvalid):
		status, message = http.StatusBadRequest, "Choose an available model."
	case errors.Is(err, aimodels.ErrDisabled):
		status, message = http.StatusConflict, err.Error()
	}
	writeJSON(w, status, map[string]string{"code": "ai_model_settings_error", "message": message})
}

func defaultAIRoutes(model, reasoning string) []aimodels.Route {
	if model == "" {
		model = agent.FrontierDefaultModelID()
	}
	if reasoning == "" {
		reasoning = "low"
	}
	// Browsing is empty by default: it follows the run's vision route.
	defaults := map[string]string{"agent": model, "vision": model, "browsing": "", "routing": envOr("MISTY_AI_LOW_MODEL", agent.DefaultAgentLowGatewayModel), "realtime": agent.AgentRealtimeModel, "embedding": envOr("SMART_LIBRARY_EMBEDDING_MODEL", agent.SmartLibraryEmbeddingModel), "transcription": envOr("AGENT_TRANSCRIPTION_MODEL", "openai/gpt-4o-mini-transcribe"), "transcription-fallback": agent.MediaSearchTranscriptionFallbackModel, "media-transcription": envOr("MEDIA_SEARCH_TRANSCRIPTION_MODEL", agent.MediaSearchTranscriptionModel), "media-transcription-fallback": envOr("MEDIA_SEARCH_TRANSCRIPTION_FALLBACK_MODEL", agent.MediaSearchTranscriptionFallbackModel), "speech": agent.AgentSpeechModel, "live-transcription": envOr("MISTY_LIVE_TRANSCRIPTION_MODEL", "soniox/stt-rt-v4"), "diarized-transcription": envOr("MISTY_DIARIZED_TRANSCRIPTION_MODEL", "soniox/stt-async-v4"), "dictation-cleanup": envOr("MISTY_AI_LOW_MODEL", agent.DefaultAgentLowGatewayModel), "voice-notes": envOr("MISTY_VOICE_NOTES_MODEL", agent.FrontierDefaultModelID()), "day-summary": envOr("MISTY_AI_LOW_MODEL", agent.DefaultAgentLowGatewayModel)}
	result := make([]aimodels.Route, 0, len(aimodels.Roles))
	for _, role := range aimodels.Roles {
		effort := ""
		if role.Reasoning {
			effort = "low"
		}
		if role.ID == "agent" {
			effort = reasoning
		}
		result = append(result, aimodels.Route{Role: role.ID, Model: defaults[role.ID], Reasoning: effort, Enabled: true})
	}
	return result
}

// defaultRoleModel is Misty's model for a role no account chooses, so callers
// never name a model themselves.
func defaultRoleModel(role string) string {
	return modelRoute(defaultAIRoutes("", ""), role).Model
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(config.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// ResolveAIModel returns the account's model for a role (resolveRoleModel),
// or nil for the operator's default.
func (s *SpacesService) ResolveAIModel(ctx context.Context, user, role string) (*aimodels.Resolved, error) {
	return resolveRoleModel(ctx, s.database, user, role)
}

// AgentRuntimeModelProvider confirms the runtime asks for the admitted model and
// hands back how it runs: {"provider":"account","reasoning",...,"credential":
// {provider,apiKey,baseURL}} on the key the run's account brought, or
// {"provider":"instance","reasoning":...} on the operator's keys. With neither
// it answers 409 model_key_needed. The response is never logged or cached.
func (s *SpacesService) AgentRuntimeModelProvider() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RuntimeRunID string `json:"runtime_run_id"`
			Role         string `json:"role"`
			Model        string `json:"model"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		runID := chi.URLParam(r, "runID")
		user, modelUser := "", ""
		if body.Role != "agent" && body.Role != "vision" {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
			return
		}
		if isAIInvocationRuntimeID(runID) {
			record, err := s.database.ValidateAIInvocationRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			user, modelUser = record.UserID, record.UserID
			runID = record.ID
		} else {
			run, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			user, modelUser = run.OwnerUserID, runModelUser(run)
			runID = run.ID
		}
		route, err := s.database.AIModelRunRoute(r.Context(), user, runID, body.Role)
		if err != nil || route.Model != body.Model {
			writeJSON(w, 409, map[string]string{"code": "model_route_changed", "message": "The requested model does not match the admitted task."})
			return
		}
		if !route.Enabled {
			writeAIProviderError(w, aimodels.ErrDisabled)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		resolved, err := modelruntime.RouteFor(r.Context(), modelUser, route.Model, route.Reasoning)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, 200, resolved)
	}
}

func modelRoute(routes []aimodels.Route, role string) aimodels.Route {
	for _, r := range routes {
		if r.Role == role {
			return r
		}
	}
	return aimodels.Route{Role: role, Enabled: true}
}

func (s *AIService) accountAgentModel(ctx context.Context, user string) (string, string, bool, error) {
	// An account's own model choice applies only where the instance allows choosing (agent.ClientModelChoice).
	if s.providerSettings == nil || !agent.ClientModelChoice() {
		return "", "", false, nil
	}
	routes, err := s.database.AIModelRoutes(ctx, user)
	if err != nil {
		return "", "", false, err
	}
	for _, r := range routes {
		if r.Role == "agent" {
			if !r.Enabled {
				return "", "", false, aimodels.ErrDisabled
			}
			if r.Model != "" {
				return r.Model, r.Reasoning, true, nil
			}
		}
	}
	return "", "", false, nil
}

func configuredEmbeddingModel(ctx context.Context, analyzer *agent.SmartLibraryAnalyzer, user string) string {
	if analyzer == nil {
		return envOr("SMART_LIBRARY_EMBEDDING_MODEL", agent.SmartLibraryEmbeddingModel)
	}
	model, err := analyzer.WithAIAccount(user).AccountEmbeddingModel(ctx)
	if err != nil {
		return ""
	}
	return model
}
