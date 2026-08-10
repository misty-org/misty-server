package agent

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

func TestingGatewayCapabilities(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	for key, value := range values {
		if enabled, ok := value.(bool); !ok || enabled {
			list = append(list, key)
		}
	}
	sort.Strings(list)
	return list
}

func normalizedCapabilities(capabilities []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		key := strings.ToLower(capability)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func firstPricingValue(pricing map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if len(pricing[key]) > 0 {
			return pricing[key]
		}
	}
	return nil
}

func TestingGatewayTokenRate(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var value string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return 0, false
		}
	} else {
		value = string(raw)
	}
	perTokenUSD, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || perTokenUSD < 0 {
		return 0, false
	}
	// USD/token × 1,000,000 tokens × 1,000 converts to the meter's
	// thousandths-of-USD-per-million-token unit.
	rate := int64(perTokenUSD*1_000_000_000 + 0.5)
	return rate, true
}

func TestingFilterChatModels(models []GatewayModel) []GatewayModel {
	seen := map[string]bool{}
	out := []GatewayModel{}
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		lower := strings.ToLower(model.ID + " " + model.Name)
		if model.ID == "" || seen[model.ID] || strings.Contains(lower, "embedding") || strings.Contains(lower, "rerank") || strings.Contains(lower, "transcrib") || strings.Contains(lower, "image") || strings.Contains(lower, "video") {
			continue
		}
		if len(model.Capabilities) > 0 && !hasTextGenerationCapability(model.Capabilities) {
			continue
		}
		seen[model.ID] = true
		if strings.TrimSpace(model.Name) == "" {
			model.Name = modelDisplayName(model.ID)
		}
		if len(model.Capabilities) == 0 {
			model.Capabilities = []string{"chat"}
		}
		out = append(out, model)
	}
	return out
}

func hasTextGenerationCapability(capabilities []string) bool {
	for _, capability := range capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "chat", "language", "text", "text_generation", "generation", "reasoning", "tools", "tool-use", "tool_calling", "function_calling", "vision":
			return true
		}
	}
	return false
}

func modelDisplayName(id string) string {
	if slash := strings.LastIndex(id, "/"); slash >= 0 && slash < len(id)-1 {
		return id[slash+1:]
	}
	return id
}
