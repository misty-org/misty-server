package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

// groundedAgentCitations drops any model citation that is not anchored to a
// location the device explicitly supplied in a successful preview_file result.
func TestingGroundedAgentCitations(request ModelRequest, citations []AgentCitation) []AgentCitation {
	sources := make([]agentCitationSource, 0, len(request.ToolResults))
	for _, result := range request.ToolResults {
		if result.Name != ToolPreviewFile || !result.OK || len(result.Result) == 0 {
			continue
		}
		var source agentCitationSource
		if json.Unmarshal(result.Result, &source) == nil && source.ScopeID != "" && source.FileName != "" && len(source.Sections) > 0 {
			sources = append(sources, source)
		}
	}
	grounded := make([]AgentCitation, 0, len(citations))
	for _, citation := range citations {
		for _, source := range sources {
			if citation.ScopeID == source.ScopeID && citation.FileName == source.FileName && citation.RelativePath == source.RelativePath && citationMatchesSourceLocation(citation, source) {
				grounded = append(grounded, citation)
				break
			}
		}
	}
	return grounded
}

func citationMatchesSourceLocation(citation AgentCitation, source agentCitationSource) bool {
	for _, section := range source.Sections {
		kind := strings.TrimSpace(section.Kind)
		locator := strings.TrimSpace(section.Locator)
		switch citation.Kind {
		case "pdf_page":
			if kind == "page" && citation.Page > 0 && locator == strconv.Itoa(citation.Page) {
				return true
			}
		case "slide":
			if kind == "slide" && citation.Slide > 0 && locator == strconv.Itoa(citation.Slide) {
				return true
			}
		case "sheet_range":
			if kind == "sheet" && locator != "" {
				sheet, cellRange, hasRange := strings.Cut(locator, "!")
				if (hasRange && strings.TrimSpace(citation.Sheet) == strings.TrimSpace(sheet) && strings.TrimSpace(citation.Range) == strings.TrimSpace(cellRange)) || strings.Contains(citation.Label, locator) {
					return true
				}
			}
		case "section":
			if (kind == "section" || kind == "lines") && locator != "" && (locator == strings.TrimSpace(citation.Section) || strings.Contains(citation.Label, locator)) {
				return true
			}
		case "image":
			if kind == "image" {
				return true
			}
		}
	}
	return false
}

func geminiAgentResponseSchema() map[string]any {
	operationSchema := map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"type":       map[string]any{"type": "STRING", "enum": []string{"mkdir", "move", "rename"}},
			"path":       map[string]any{"type": "STRING"},
			"from":       map[string]any{"type": "STRING"},
			"to":         map[string]any{"type": "STRING"},
			"reason":     map[string]any{"type": "STRING"},
			"confidence": map[string]any{"type": "NUMBER"},
		},
	}
	return map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"text": map[string]any{"type": "STRING"},
			"tool_requests": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"id":        map[string]any{"type": "STRING"},
						"name":      map[string]any{"type": "STRING"},
						"risk":      map[string]any{"type": "STRING", "enum": []string{RiskRead, RiskWrite, RiskDangerous}},
						"arguments": map[string]any{"type": "OBJECT"},
					},
				},
			},
			"file_plan": map[string]any{
				"type":     "OBJECT",
				"nullable": true,
				"properties": map[string]any{
					"summary":            map[string]any{"type": "STRING"},
					"completion_summary": map[string]any{"type": "STRING"},
					"operations":         map[string]any{"type": "ARRAY", "items": operationSchema},
					"warnings":           map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
				},
			},
		},
	}
}
