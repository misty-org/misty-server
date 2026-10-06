package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/modelruntime"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	InitialSelectedModelID   = DefaultFrontierModelID
	InitialSelectedModelName = "GPT-6 Astra"
)

var ErrModelUnavailable = errors.New("agent model unavailable")

type GatewayModel struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	// ContextWindow is the model's input limit in tokens, when the Gateway
	// reports one.
	ContextWindow int `json:"context_window,omitempty"`
}

// DefaultContextWindow is assumed for models whose window is unknown, such as
// an account's own non-Gateway models. It is deliberately conservative.
const DefaultContextWindow = 128_000

// ModelContextWindow is the context window runs compact against.
func ModelContextWindow(ctx context.Context, modelID string) int {
	models, err := GatewayModels(ctx)
	if err != nil {
		return DefaultContextWindow
	}
	for _, model := range models {
		if model.ID == strings.TrimSpace(modelID) && model.ContextWindow > 0 {
			return model.ContextWindow
		}
	}
	return DefaultContextWindow
}

var gatewayCatalogCache struct {
	sync.Mutex
	models    []GatewayModel
	expiresAt time.Time
}

func GatewayModels(ctx context.Context) ([]GatewayModel, error) {
	if _, err := envconfig.AgentModel(); err != nil {
		return nil, err
	}
	gatewayCatalogCache.Lock()
	if time.Now().Before(gatewayCatalogCache.expiresAt) && len(gatewayCatalogCache.models) > 0 {
		models := append([]GatewayModel(nil), gatewayCatalogCache.models...)
		gatewayCatalogCache.Unlock()
		return models, nil
	}
	gatewayCatalogCache.Unlock()

	models, err := TestingFetchGatewayModels(ctx)
	if err != nil || len(models) == 0 {
		models = configuredGatewayModels()
	}
	if len(models) == 0 {
		return nil, errors.New("gateway model catalog is unavailable")
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name) })
	gatewayCatalogCache.Lock()
	gatewayCatalogCache.models = append([]GatewayModel(nil), models...)
	gatewayCatalogCache.expiresAt = time.Now().Add(10 * time.Minute)
	gatewayCatalogCache.Unlock()
	return models, nil
}

func GatewayModelAvailable(ctx context.Context, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	models, err := GatewayModels(ctx)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID == modelID {
			return true
		}
	}
	return false
}

// GatewayModelSupportsReasoning reports whether a model exposes adjustable
// reasoning effort, based on the capabilities the gateway advertises. Keep the
// capability strings in sync with modelSupportsReasoning on the client.
func GatewayModelSupportsReasoning(ctx context.Context, modelID string) bool {
	models, err := GatewayModels(ctx)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID != strings.TrimSpace(modelID) {
			continue
		}
		for _, capability := range model.Capabilities {
			switch strings.ToLower(strings.TrimSpace(capability)) {
			case "reasoning", "thinking", "reasoning-effort":
				return true
			}
		}
		return false
	}
	return false
}

func NewGatewayProviderForModelWithReasoning(models *modelruntime.Client, modelID, reasoningEffort string) (ModelProvider, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || strings.ContainsAny(modelID, "\r\n\t ") || len(modelID) > 200 {
		return nil, errors.New("invalid gateway model")
	}
	if !models.Enabled() {
		return nil, modelruntime.ErrUnavailable
	}
	return NewRuntimeProvider(models, modelruntime.Instance(), modelID, ProviderVercelAI, reasoningEffort), nil
}

func configuredGatewayModels() []GatewayModel {
	var configured []GatewayModel
	if json.Unmarshal([]byte(strings.TrimSpace(envconfig.Getenv("MISTY_AI_MODEL_CATALOG_JSON"))), &configured) == nil {
		return TestingFilterChatModels(configured)
	}
	ids := []string{
		envOrDefault("MISTY_AI_LOW_MODEL", TestingDefaultAgentLowGatewayModel),
		envOrDefault("MISTY_AI_MED_MODEL", TestingDefaultAgentMedGatewayModel),
		envOrDefault("MISTY_AI_HIGH_MODEL", TestingDefaultAgentHighGatewayModel),
	}
	seen := map[string]bool{}
	out := []GatewayModel{{ID: DefaultFrontierModelID, Name: "GPT-6 Astra", Capabilities: []string{"chat", "tools", "vision", "reasoning"}}}
	seen[DefaultFrontierModelID] = true
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, GatewayModel{ID: id, Name: modelDisplayName(id), Capabilities: []string{"chat", "tools"}})
	}
	return out
}

// gatewayCatalogItem is one entry of the Gateway's public model list.
type gatewayCatalogItem struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Tags          []string        `json:"tags"`
	Capabilities  json.RawMessage `json:"capabilities"`
	ContextWindow int             `json:"context_window"`
}

func TestingFetchGatewayModels(ctx context.Context) ([]GatewayModel, error) {
	items, err := fetchGatewayCatalog(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]GatewayModel, 0, len(items))
	for _, item := range items {
		itemType := strings.ToLower(strings.TrimSpace(item.Type))
		if itemType != "" && !strings.Contains(itemType, "language") && !strings.Contains(itemType, "chat") && !strings.Contains(itemType, "text") {
			continue
		}
		capabilities := TestingGatewayCapabilities(item.Capabilities)
		if len(item.Tags) > 0 {
			capabilities = append(capabilities, item.Tags...)
		}
		// The endpoint's type is authoritative. Adding it to the normalized
		// capabilities keeps language models with specialized tags (for example
		// web search only) in the chat catalog.
		capabilities = append(capabilities, "language")
		models = append(models, GatewayModel{
			ID: item.ID, Name: item.Name, Capabilities: normalizedCapabilities(capabilities), ContextWindow: item.ContextWindow,
		})
	}
	return TestingFilterChatModels(models), nil
}

// fetchGatewayCatalog reads the Gateway's full public model list.
func fetchGatewayCatalog(ctx context.Context) ([]gatewayCatalogItem, error) {
	apiKey := firstEnv("AI_GATEWAY_API_KEY", "VERCEL_OIDC_TOKEN")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(envOrDefault("AI_GATEWAY_BASE_URL", TestingDefaultVercelAIBaseURL), "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	// Vercel intentionally exposes model discovery without authentication. Keep
	// sending credentials when configured so private/custom gateway endpoints
	// remain compatible, but do not collapse the public catalog to three local
	// fallback models just because this process has no inference key.
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := defaultHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("gateway model catalog request failed")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []gatewayCatalogItem `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}
