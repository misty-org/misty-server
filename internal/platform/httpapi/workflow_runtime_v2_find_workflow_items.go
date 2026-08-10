package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func TestingFindWorkflowItems(value any) []any {
	switch item := value.(type) {
	case []any:
		return item
	case map[string]any:
		for _, key := range []string{"events", "items"} {
			if values, ok := item[key].([]any); ok {
				return values
			}
		}
		for _, child := range item {
			if values := TestingFindWorkflowItems(child); values != nil {
				return values
			}
		}
	}
	return nil
}

func TestingNormalizeContentPage(run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var input map[string]any
	if json.Unmarshal(invocation.Input, &input) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	ref, text := findContentRefAndText(input)
	if strings.TrimSpace(text) == "" {
		return nil, workflowv2.ErrUnsupportedContent
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	if ref.SourceKind == "" {
		ref = workflowv2.ContentRef{SourceKind: "inline", ProviderID: "misty", ResourceID: run.ID + ":" + invocation.NodeID, Fingerprint: digest, DisplayName: "Workflow input", MIMEType: "text/plain", PermissionScope: "run:" + run.ID}
	}
	pageSize := 50
	var config struct {
		PageSize int `json:"pageSize"`
	}
	_ = json.Unmarshal(invocation.Config, &config)
	if config.PageSize > 0 && config.PageSize <= 100 {
		pageSize = config.PageSize
	}
	cursor := 0
	if raw, ok := input["cursor"].(string); ok {
		cursor, _ = strconv.Atoi(raw)
	}
	chunks := chunkWorkflowText(text, 4000)
	if cursor < 0 || cursor > len(chunks) {
		return nil, workflowv2.ErrOutputInvalid
	}
	end := cursor + pageSize
	if end > len(chunks) {
		end = len(chunks)
	}
	sections := make([]workflowv2.ContentSection, 0, end-cursor)
	citations := make([]workflowv2.Citation, 0, end-cursor)
	for index := cursor; index < end; index++ {
		locator := fmt.Sprintf("section:%d", index+1)
		sections = append(sections, workflowv2.ContentSection{Kind: "text", Locator: locator, Text: chunks[index]})
		citations = append(citations, workflowv2.Citation{Content: ref, Kind: "section", Locator: locator})
	}
	next := ""
	if end < len(chunks) {
		next = strconv.Itoa(end)
	}
	page := workflowv2.ContentPage{Content: ref, Sections: sections, Citations: citations, NextCursor: next, Truncated: next != "", SourceChanged: ref.Fingerprint != "" && ref.Fingerprint != digest}
	return TestingMustAPIRawJSON(page), nil
}

func findContentRefAndText(input map[string]any) (workflowv2.ContentRef, string) {
	var ref workflowv2.ContentRef
	for _, key := range []string{"contentRef", "ref", "content"} {
		if object, ok := input[key].(map[string]any); ok {
			raw, _ := json.Marshal(object)
			_ = json.Unmarshal(raw, &ref)
		}
	}
	for _, key := range []string{"text", "body", "message", "content"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return ref, value
		}
	}
	for _, value := range input {
		if object, ok := value.(map[string]any); ok {
			nestedRef, nestedText := findContentRefAndText(object)
			if ref.SourceKind == "" {
				ref = nestedRef
			}
			if nestedText != "" {
				return ref, nestedText
			}
		}
	}
	return ref, ""
}

func chunkWorkflowText(value string, maximum int) []string {
	runes := []rune(value)
	if len(runes) == 0 {
		return nil
	}
	out := make([]string, 0, (len(runes)+maximum-1)/maximum)
	for start := 0; start < len(runes); start += maximum {
		end := start + maximum
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}

func TestingDecodeJSONObject(value string) json.RawMessage {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	var object map[string]any
	if json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
		return nil
	}
	return json.RawMessage(trimmed)
}

func extractWorkflowText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var find func(any) string
	find = func(current any) string {
		switch item := current.(type) {
		case string:
			return strings.TrimSpace(item)
		case map[string]any:
			for _, key := range []string{"text", "answer", "summary", "body"} {
				if result := find(item[key]); result != "" {
					return result
				}
			}
		case []any:
			for _, child := range item {
				if result := find(child); result != "" {
					return result
				}
			}
		}
		return ""
	}
	return find(value)
}

func workflowToolEligible(descriptor workflowv2.NodeDescriptor, declared map[string]workflowv2.Risk) bool {
	granted, ok := declared[descriptor.Capability]
	if !ok || workflowRiskRank(granted) < workflowRiskRank(descriptor.Risk) || descriptor.Risk == workflowv2.RiskDestructive {
		return false
	}
	switch descriptor.Kind {
	case "manual_trigger", "chat_trigger", "cron_trigger", "file_changes", "library_changes", "message_trigger", "connector_trigger", "transform", "for_each", "condition", "switch", "join", "debounce", "delay", "call_workflow", "agent_task":
		return false
	default:
		return true
	}
}

func workflowRiskRank(risk workflowv2.Risk) int {
	if risk == workflowv2.RiskDestructive {
		return 3
	}
	if risk == workflowv2.RiskWrite {
		return 2
	}
	return 1
}

func agentToolRisk(risk workflowv2.Risk) string {
	if risk == workflowv2.RiskDestructive {
		return serveragent.RiskDangerous
	}
	if risk == workflowv2.RiskWrite {
		return serveragent.RiskWrite
	}
	return serveragent.RiskRead
}

func workflowToolArguments(raw json.RawMessage) (json.RawMessage, json.RawMessage) {
	config := json.RawMessage(`{}`)
	input := raw
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return config, input
	}
	if value, ok := object["config"].(map[string]any); ok {
		config, _ = json.Marshal(value)
	}
	if value, exists := object["input"]; exists {
		input, _ = json.Marshal(value)
	}
	return config, input
}

func TestingWorkflowResourceIdentity(config, input json.RawMessage) (string, string) {
	var configValue, inputValue any
	_ = json.Unmarshal(config, &configValue)
	_ = json.Unmarshal(input, &inputValue)
	var find func(any) (string, string)
	find = func(value any) (string, string) {
		switch item := value.(type) {
		case map[string]any:
			provider, _ := item["providerId"].(string)
			resource, _ := item["resourceId"].(string)
			fingerprint, _ := item["fingerprint"].(string)
			if resource != "" {
				return provider + ":" + resource, fingerprint
			}
			for _, key := range []string{"destination", "relativePath", "channelId", "threadId"} {
				if text, ok := item[key].(string); ok && strings.TrimSpace(text) != "" {
					return key + ":" + strings.TrimSpace(text), fingerprint
				}
			}
			for _, child := range item {
				if key, childFingerprint := find(child); key != "" {
					return key, childFingerprint
				}
			}
		case []any:
			for _, child := range item {
				if key, childFingerprint := find(child); key != "" {
					return key, childFingerprint
				}
			}
		}
		return "", ""
	}
	if key, fingerprint := find(inputValue); key != "" {
		return key, fingerprint
	}
	return find(configValue)
}
