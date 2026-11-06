package agent

import "strings"

func FilterChatModels(models []GatewayModel) []GatewayModel {
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
