package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/aimodels"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) aiModelsUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, err := sessionUserID(r, s.database)
	if err != nil || user == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return "", false
	}
	return user, true
}

func writeAIProviderError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "Could not save the model choice. Try again."
	switch {
	case errors.Is(err, db.ErrSpaceInvalid):
		status, message = http.StatusBadRequest, "Choose an available model."
	case errors.Is(err, aimodels.ErrDisabled):
		status, message = http.StatusConflict, err.Error()
	}
	writeJSON(w, status, map[string]string{"code": "ai_model_settings_error", "message": message})
}

type aiSense struct {
	aimodels.Sense
	// Model is the account's choice; empty means Misty's default.
	Model        string             `json:"model"`
	DefaultModel string             `json:"default_model"`
	Options      []agent.SenseModel `json:"options"`
}

// AIModelSenses lists each sense with the account's choice and the models that
// fit it. A Gateway outage leaves the options empty, never the page.
func (s *SpacesService) AIModelSenses() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.aiModelsUser(w, r)
		if !ok {
			return
		}
		routes, err := s.database.AIModelRoutes(r.Context(), user)
		if err != nil {
			writeAIProviderError(w, err)
			return
		}
		defaults := defaultAIRoutes("", "")
		senses := make([]aiSense, 0, len(aimodels.Senses))
		for _, sense := range aimodels.Senses {
			primary := sense.Roles[0]
			options, catalogErr := agent.SenseModels(r.Context(), sense.Kind)
			if catalogErr != nil {
				options = []agent.SenseModel{}
			}
			senses = append(senses, aiSense{Sense: sense, Model: modelRoute(routes, primary).Model, DefaultModel: modelRoute(defaults, primary).Model, Options: options})
		}
		writeJSON(w, http.StatusOK, map[string]any{"senses": senses})
	}
}

// AIModelSense sets one sense's model. An empty model returns it to the default.
func (s *SpacesService) AIModelSense() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.aiModelsUser(w, r)
		if !ok {
			return
		}
		sense, found := aimodels.FindSense(chi.URLParam(r, "sense"))
		if !found {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if TestingDecodeAIJSONWithLimit(w, r, &body, 4<<10) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		body.Model = strings.TrimSpace(body.Model)
		if body.Model != "" && !agent.SenseModelAvailable(r.Context(), sense.Kind, body.Model) {
			writeAIProviderError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.SaveAIModelSense(r.Context(), user, sense, body.Model); err != nil {
			writeAIProviderError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func defaultAIRoutes(model, reasoning string) []aimodels.Route {
	if model == "" {
		model = agent.FrontierDefaultModelID()
	}
	if reasoning == "" {
		reasoning = "low"
	}
	defaults := map[string]string{"agent": model, "vision": model, "routing": envOr("MISTY_AI_LOW_MODEL", agent.TestingDefaultAgentLowGatewayModel), "realtime": agent.AgentRealtimeModel, "library": envOr("SMART_LIBRARY_PRIMARY_MODEL", agent.SmartLibraryPrimaryModel), "library-fallback": envOr("SMART_LIBRARY_FALLBACK_MODEL", agent.SmartLibraryFallbackModel), "embedding": envOr("SMART_LIBRARY_EMBEDDING_MODEL", agent.SmartLibraryEmbeddingModel), "transcription": envOr("AGENT_TRANSCRIPTION_MODEL", "openai/gpt-4o-mini-transcribe"), "transcription-fallback": agent.MediaSearchTranscriptionFallbackModel, "media-transcription": envOr("MEDIA_SEARCH_TRANSCRIPTION_MODEL", agent.MediaSearchTranscriptionModel), "media-transcription-fallback": envOr("MEDIA_SEARCH_TRANSCRIPTION_FALLBACK_MODEL", agent.MediaSearchTranscriptionFallbackModel), "speech": agent.AgentSpeechModel}
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
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(envconfig.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// ResolveAIModel returns the account's chosen model for a role, or nil for
// Misty's default.
func (s *SpacesService) ResolveAIModel(ctx context.Context, user, role string) (*aimodels.Resolved, error) {
	routes, err := s.database.AIModelRoutes(ctx, user)
	if err != nil {
		return nil, err
	}
	r := modelRoute(routes, role)
	if !r.Enabled {
		return nil, aimodels.ErrDisabled
	}
	if r.Model == "" {
		return nil, nil
	}
	return &aimodels.Resolved{Model: r.Model, Reasoning: r.Reasoning}, nil
}

// AgentRuntimeModelProvider confirms the runtime asks for the admitted model and
// hands back the run's reasoning. Keys never leave the runtime's environment.
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
		user := ""
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
			user = record.UserID
			runID = record.ID
		} else {
			run, _, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			user = run.OwnerUserID
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
		writeJSON(w, 200, map[string]any{"provider": "instance", "reasoning": route.Reasoning})
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
	if s.providerSettings == nil {
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

func (s *SpacesService) runtimeModelBilling(ctx context.Context, user, run, node, fallback string) (string, string, error) {
	role := "agent"
	if db.ScreenPlanningNode(node) {
		role = "vision"
	}
	r, err := s.database.AIModelRunRoute(ctx, user, run, role)
	if err != nil {
		return "", "", err
	}
	if !r.Enabled {
		return "", "", aimodels.ErrDisabled
	}
	if r.Model == "" {
		r.Model = fallback
	}
	return aimodels.UsageProvider(r.Model), r.Model, nil
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
