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

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	GatewayModelCatalogVersion = "gateway-live-v2"
	InitialSelectedModelID     = "poolside/laguna-s-2.1-free"
	InitialSelectedModelName   = "Laguna S 2.1 Free"
)

var ErrModelUnavailable = errors.New("agent model unavailable")

type GatewayModel struct {
	ID                           string   `json:"id"`
	Name                         string   `json:"name"`
	Capabilities                 []string `json:"capabilities"`
	InputRateMilliUSDPerMillion  int64    `json:"-"`
	CachedRateMilliUSDPerMillion int64    `json:"-"`
	OutputRateMilliUSDPerMillion int64    `json:"-"`
	HasTokenPricing              bool     `json:"-"`
}

var gatewayCatalogCache struct {
	sync.Mutex
	models    []GatewayModel
	expiresAt time.Time
}

func GatewayModels(ctx context.Context) ([]GatewayModel, error) {
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

func GatewayModelSupportsTools(ctx context.Context, modelID string) bool {
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
			case "tools", "tool-use", "tool_calling", "function_calling":
				return true
			}
		}
		return false
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

// CachedGatewayModelRates exposes server-only gateway pricing to the usage
// meter without sending prices to clients. Values are thousandths of a dollar
// per million tokens, matching the versioned Hosted AI rate-card units.
func CachedGatewayModelRates(modelID string) (input, cachedInput, output int64, ok bool) {
	gatewayCatalogCache.Lock()
	defer gatewayCatalogCache.Unlock()
	for _, model := range gatewayCatalogCache.models {
		if model.ID == strings.TrimSpace(modelID) && model.HasTokenPricing {
			cached := model.CachedRateMilliUSDPerMillion
			if cached <= 0 {
				cached = model.InputRateMilliUSDPerMillion
			}
			return model.InputRateMilliUSDPerMillion, cached, model.OutputRateMilliUSDPerMillion, true
		}
	}
	return 0, 0, 0, false
}

func NewGatewayProviderForModel(modelID string) (ModelProvider, error) {
	return NewGatewayProviderForModelWithReasoning(modelID, "")
}

func NewGatewayProviderForModelWithReasoning(modelID, reasoningEffort string) (ModelProvider, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || strings.ContainsAny(modelID, "\r\n\t ") || len(modelID) > 200 {
		return nil, errors.New("invalid gateway model")
	}
	apiKey := firstEnv("AI_GATEWAY_API_KEY", "VERCEL_OIDC_TOKEN")
	if apiKey == "" {
		return nil, errors.New("AI gateway is not configured")
	}
	return NewOpenAIProvider(OpenAIProviderConfig{APIKey: apiKey, BaseURL: envOrDefault("AI_GATEWAY_BASE_URL", TestingDefaultVercelAIBaseURL), Model: modelID, ProviderName: ProviderVercelAI, ReasoningEffort: strings.TrimSpace(reasoningEffort)}), nil
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
	out := []GatewayModel{}
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

func TestingFetchGatewayModels(ctx context.Context) ([]GatewayModel, error) {
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
		Data []struct {
			ID           string                     `json:"id"`
			Name         string                     `json:"name"`
			Type         string                     `json:"type"`
			Tags         []string                   `json:"tags"`
			Capabilities json.RawMessage            `json:"capabilities"`
			Pricing      map[string]json.RawMessage `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make([]GatewayModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		itemType := strings.ToLower(strings.TrimSpace(item.Type))
		if itemType != "" && !strings.Contains(itemType, "language") && !strings.Contains(itemType, "chat") && !strings.Contains(itemType, "text") {
			continue
		}
		input, inputOK := TestingGatewayTokenRate(item.Pricing["input"])
		output, outputOK := TestingGatewayTokenRate(item.Pricing["output"])
		cached, _ := TestingGatewayTokenRate(firstPricingValue(item.Pricing, "cached_input", "input_cache_read", "cache_read"))
		capabilities := TestingGatewayCapabilities(item.Capabilities)
		if len(item.Tags) > 0 {
			capabilities = append(capabilities, item.Tags...)
		}
		// The endpoint's type is authoritative. Adding it to the normalized
		// capabilities keeps language models with specialized tags (for example
		// web search only) in the chat catalog.
		capabilities = append(capabilities, "language")
		models = append(models, GatewayModel{
			ID: item.ID, Name: item.Name, Capabilities: normalizedCapabilities(capabilities),
			InputRateMilliUSDPerMillion: input, CachedRateMilliUSDPerMillion: cached,
			OutputRateMilliUSDPerMillion: output, HasTokenPricing: inputOK && outputOK,
		})
	}
	return TestingFilterChatModels(models), nil
}
