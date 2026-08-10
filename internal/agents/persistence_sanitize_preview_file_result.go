package agent

import (
	"encoding/json"
	"strings"
)

// Extracted native document text follows the short-lived file-content
// policy, not the 30-day conversation policy. Persist only citation
// coordinates and harmless document metadata needed to explain an answer.
func sanitizePreviewFileResult(raw json.RawMessage) json.RawMessage {
	var document struct {
		DocumentID   string `json:"documentId"`
		FileName     string `json:"fileName"`
		MimeType     string `json:"mimeType"`
		ScopeID      string `json:"scopeId"`
		RelativePath string `json:"relativePath"`
		Sections     []struct {
			Kind    string `json:"kind"`
			Locator string `json:"locator"`
		} `json:"sections"`
		Truncated bool `json:"truncated"`
	}
	if json.Unmarshal(raw, &document) != nil {
		return nil
	}
	cleaned, err := json.Marshal(document)
	if err != nil {
		return nil
	}
	return sanitizeRawJSON(cleaned)
}

func sanitizeAgentEvents(events []AgentEvent) []AgentEvent {
	out := make([]AgentEvent, len(events))
	for i, event := range events {
		out[i] = sanitizeAgentEvent(event)
	}
	return out
}

func sanitizeAgentEvent(event AgentEvent) AgentEvent {
	event.Text = redactLocalPaths(event.Text)
	event.Message = redactLocalPaths(event.Message)
	if event.FilePlan != nil {
		plan := *event.FilePlan
		plan.Operations = append([]FileOperation(nil), plan.Operations...)
		for index := range plan.Operations {
			plan.Operations[index].Path = ""
			plan.Operations[index].From = ""
			plan.Operations[index].To = ""
		}
		event.FilePlan = &plan
	}
	event.ToolRequests = append([]ToolRequest(nil), event.ToolRequests...)
	for index := range event.ToolRequests {
		event.ToolRequests[index].Arguments = sanitizeRawJSON(event.ToolRequests[index].Arguments)
	}
	event.Citations = append([]AgentCitation(nil), event.Citations...)
	for index := range event.Citations {
		event.Citations[index].Excerpt = redactLocalPaths(event.Citations[index].Excerpt)
	}
	return event
}

func sanitizeRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	value = sanitizeJSONValue(value)
	cleaned, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return cleaned
}

func sanitizeJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			switch normalized {
			case "imagedataurl", "imageurl", "dataurl", "activeroot", "absolutepath", "localpath", "path", "from", "to":
				continue
			}
			out[key] = sanitizeJSONValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = sanitizeJSONValue(child)
		}
		return out
	case string:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(typed)), "data:image/") {
			return "[image omitted]"
		}
		return redactLocalPaths(typed)
	default:
		return value
	}
}

func redactLocalPaths(value string) string {
	value = unixLocalPathPattern.ReplaceAllString(value, "[local path]")
	return windowsLocalPathPattern.ReplaceAllString(value, "[local path]")
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
