package agent

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

func filePlanGenAISchema(nullable bool) *genai.Schema {
	return &genai.Schema{
		Type:        genai.TypeObject,
		Nullable:    boolPointer(nullable),
		Description: "A safe Misty file operation plan, or null when more context is needed.",
		Properties: map[string]*genai.Schema{
			"summary": {
				Type:        genai.TypeString,
				Description: "Future-tense summary of what Misty will do before Apply.",
			},
			"completion_summary": {
				Type:        genai.TypeString,
				Description: "Past-tense summary of what Misty did after Apply.",
			},
			"operations": {
				Type:  genai.TypeArray,
				Items: fileOperationGenAISchema(),
			},
			"warnings": {
				Type:  genai.TypeArray,
				Items: &genai.Schema{Type: genai.TypeString},
			},
		},
		Required: []string{"summary", "completion_summary", "operations", "warnings"},
	}
}

func fileOperationGenAISchema() *genai.Schema {
	return &genai.Schema{
		Type:        genai.TypeObject,
		Description: "One mkdir, move, or rename operation using paths relative to active_root.",
		Properties: map[string]*genai.Schema{
			"type": {
				Type: genai.TypeString,
				Enum: []string{"mkdir", "move", "rename"},
			},
			"path": {
				Type:        genai.TypeString,
				Description: "Relative folder path for mkdir, or empty string when unused.",
			},
			"from": {
				Type:        genai.TypeString,
				Description: "Relative source path for move or rename, or empty string when unused.",
			},
			"to": {
				Type:        genai.TypeString,
				Description: "Relative destination path for move or rename, or empty string when unused.",
			},
			"reason": {
				Type:        genai.TypeString,
				Description: "Short user-facing reason.",
			},
			"confidence": {
				Type:        genai.TypeNumber,
				Description: "Confidence from 0 to 1.",
			},
		},
		Required: []string{"type", "path", "from", "to", "reason", "confidence"},
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func normalizeToolRequests(requests []ToolRequest) []ToolRequest {
	if len(requests) == 0 {
		return nil
	}
	normalized := make([]ToolRequest, 0, len(requests))
	for _, request := range requests {
		request.Name = strings.TrimSpace(request.Name)
		if request.Name == "" {
			continue
		}
		if request.ID == "" {
			request.ID = uuid.NewString()
		}
		request.Risk = normalizeRisk(request.Risk)
		if len(request.Arguments) == 0 {
			request.Arguments = json.RawMessage(`{}`)
		}
		normalized = append(normalized, request)
	}
	return normalized
}

func trimJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```JSON")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func TestingAgentResponseJSONSchema() map[string]any {
	operationSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type":       map[string]any{"type": "string", "enum": []string{"mkdir", "move", "rename"}},
			"path":       map[string]any{"type": "string"},
			"from":       map[string]any{"type": "string"},
			"to":         map[string]any{"type": "string"},
			"reason":     map[string]any{"type": "string"},
			"confidence": map[string]any{"type": "number"},
		},
		"required": []string{"type", "path", "from", "to", "reason", "confidence"},
	}
	filePlanSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"summary":            map[string]any{"type": "string"},
			"completion_summary": map[string]any{"type": "string"},
			"operations":         map[string]any{"type": "array", "items": operationSchema},
			"warnings":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"summary", "completion_summary", "operations", "warnings"},
	}
	citationSchema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"id": map[string]any{"type": "string"}, "scopeId": map[string]any{"type": "string"}, "fileName": map[string]any{"type": "string"},
		"relativePath": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string", "enum": []string{"pdf_page", "slide", "sheet_range", "section", "image"}},
		"label": map[string]any{"type": "string"}, "page": map[string]any{"type": "integer"}, "slide": map[string]any{"type": "integer"},
		"sheet": map[string]any{"type": "string"}, "range": map[string]any{"type": "string"}, "section": map[string]any{"type": "string"}, "excerpt": map[string]any{"type": "string"},
	}, "required": []string{"id", "scopeId", "fileName", "relativePath", "kind", "label", "page", "slide", "sheet", "range", "section", "excerpt"}}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"text": map[string]any{"type": "string"},
			"tool_requests": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id":        map[string]any{"type": "string"},
						"name":      map[string]any{"type": "string"},
						"risk":      map[string]any{"type": "string", "enum": []string{RiskRead, RiskWrite, RiskDangerous}},
						"arguments": map[string]any{"type": "object"},
					},
					"required": []string{"id", "name", "risk", "arguments"},
				},
			},
			"file_plan": filePlanSchema,
			"citations": map[string]any{"type": "array", "items": citationSchema},
		},
		"required": []string{"text", "tool_requests", "file_plan", "citations"},
	}
}

func sanitizeAgentPromptImages(request ModelRequest) (ModelRequest, []agentPromptImage) {
	request.ToolResults = append([]ToolResult(nil), request.ToolResults...)
	images := make([]agentPromptImage, 0, 8)
	for index := range request.ToolResults {
		if request.ToolResults[index].Name != ToolPreviewFile || len(request.ToolResults[index].Result) == 0 {
			continue
		}
		var value any
		if json.Unmarshal(request.ToolResults[index].Result, &value) != nil {
			continue
		}
		collectAndStripPromptImages(value, "document", &images)
		request.ToolResults[index].Result, _ = json.Marshal(value)
	}
	return request, images
}

func collectAndStripPromptImages(value any, context string, images *[]agentPromptImage) {
	switch typed := value.(type) {
	case map[string]any:
		if name, ok := typed["fileName"].(string); ok && strings.TrimSpace(name) != "" {
			context = name
		}
		kind, _ := typed["kind"].(string)
		locator, _ := typed["locator"].(string)
		if dataURL, ok := typed["imageDataUrl"].(string); ok && strings.HasPrefix(dataURL, "data:image/") {
			label := strings.TrimSpace(context + " " + kind + " " + locator)
			if len(*images) < 8 {
				*images = append(*images, agentPromptImage{Label: label, DataURL: dataURL})
			}
			typed["imageDataUrl"] = "[attached as multimodal image: " + label + "]"
		}
		for _, child := range typed {
			collectAndStripPromptImages(child, context, images)
		}
	case []any:
		for _, child := range typed {
			collectAndStripPromptImages(child, context, images)
		}
	}
}

func normalizeAgentCitations(values []AgentCitation) []AgentCitation {
	result := make([]AgentCitation, 0, len(values))
	for _, citation := range values {
		citation.ScopeID = strings.TrimSpace(citation.ScopeID)
		citation.FileName = strings.TrimSpace(citation.FileName)
		citation.RelativePath = strings.TrimSpace(citation.RelativePath)
		citation.Label = strings.TrimSpace(citation.Label)
		if citation.ScopeID == "" || citation.FileName == "" || citation.Label == "" || strings.HasPrefix(citation.RelativePath, "/") || strings.Contains(citation.RelativePath, "..") {
			continue
		}
		if citation.ID == "" {
			citation.ID = uuid.NewString()
		}
		if len(citation.Excerpt) > 500 {
			citation.Excerpt = citation.Excerpt[:500]
		}
		result = append(result, citation)
		if len(result) == 30 {
			break
		}
	}
	return result
}

type agentCitationSource struct {
	ScopeID      string `json:"scopeId"`
	FileName     string `json:"fileName"`
	RelativePath string `json:"relativePath"`
	Sections     []struct {
		Kind    string `json:"kind"`
		Locator string `json:"locator"`
	} `json:"sections"`
}
