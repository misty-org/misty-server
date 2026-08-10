package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func TestingFindWorkflowString(value any, keys ...string) string {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var find func(any) string
	find = func(current any) string {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				if wanted[key] {
					if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
						return strings.TrimSpace(text)
					}
				}
			}
			for _, child := range item {
				if found := find(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range item {
				if found := find(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return find(value)
}

func findWorkflowStrings(value any, key string) []string {
	var find func(any) []string
	find = func(current any) []string {
		switch item := current.(type) {
		case map[string]any:
			if values, ok := item[key].([]any); ok {
				out := make([]string, 0, len(values))
				for _, value := range values {
					if text, ok := value.(string); ok {
						out = append(out, text)
					}
				}
				return out
			}
			for _, child := range item {
				if found := find(child); len(found) > 0 {
					return found
				}
			}
		case []any:
			for _, child := range item {
				if found := find(child); len(found) > 0 {
					return found
				}
			}
		}
		return nil
	}
	return find(value)
}

func TestingEvaluateControlBranch(kind string, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var input any
	if json.Unmarshal(invocation.Input, &input) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	var config struct {
		Path     string         `json:"path"`
		Operator string         `json:"operator"`
		Value    any            `json:"value"`
		Cases    map[string]any `json:"cases"`
	}
	if json.Unmarshal(invocation.Config, &config) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	selected := input
	if strings.TrimSpace(config.Path) != "" {
		var found bool
		selected, found = workflowPath(input, config.Path)
		if !found && kind == "condition" && config.Operator != "not_exists" {
			selected = nil
		}
	}
	if kind == "switch" {
		caseNames := make([]string, 0, len(config.Cases))
		for name := range config.Cases {
			caseNames = append(caseNames, name)
		}
		sort.Strings(caseNames)
		port := "default"
		for _, name := range caseNames {
			if workflowValuesEqual(selected, config.Cases[name]) {
				port = name
				break
			}
		}
		return TestingMustAPIRawJSON(map[string]any{"selected": port, port: input}), nil
	}
	operator := config.Operator
	if operator == "" {
		operator = "equals"
	}
	matched := false
	switch operator {
	case "exists":
		matched = selected != nil
	case "not_exists":
		matched = selected == nil
	case "equals":
		matched = workflowValuesEqual(selected, config.Value)
	case "not_equals":
		matched = !workflowValuesEqual(selected, config.Value)
	case "contains":
		matched = strings.Contains(fmt.Sprint(selected), fmt.Sprint(config.Value))
	case "gt", "gte", "lt", "lte":
		left, leftOK := selected.(float64)
		right, rightOK := config.Value.(float64)
		if leftOK && rightOK {
			switch operator {
			case "gt":
				matched = left > right
			case "gte":
				matched = left >= right
			case "lt":
				matched = left < right
			case "lte":
				matched = left <= right
			}
		}
	default:
		return nil, workflowv2.ErrOutputInvalid
	}
	port := "false"
	if matched {
		port = "true"
	}
	return TestingMustAPIRawJSON(map[string]any{"matched": matched, port: input}), nil
}

func workflowPath(root any, path string) (any, bool) {
	path = strings.Trim(strings.ReplaceAll(path, "/", "."), ".")
	if path == "" {
		return root, true
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		switch item := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = item[segment]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(item) {
				return nil, false
			}
			current = item[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func workflowValuesEqual(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func safeGeneratedArtifactName(agentName, nodeID string) string {
	clean := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		var out strings.Builder
		for _, char := range value {
			if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
				out.WriteRune(char)
			} else if out.Len() > 0 && !strings.HasSuffix(out.String(), "-") {
				out.WriteByte('-')
			}
		}
		return strings.Trim(out.String(), "-")
	}
	name := clean(agentName)
	if name == "" {
		name = "agent"
	}
	node := clean(nodeID)
	if node == "" {
		node = "result"
	}
	return name + "-" + node + ".md"
}

func (s *SpacesService) changedFilesNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	if run.AgentInstanceID == "" || run.WorkflowVersionID == "" {
		return nil, workflowv2.ErrOutputInvalid
	}
	var input map[string]any
	if json.Unmarshal(invocation.Input, &input) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	rawItems := TestingFindWorkflowItems(input)
	claimed := make([]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		eventID, _ := item["eventId"].(string)
		if eventID == "" {
			eventID, _ = item["event_id"].(string)
		}
		provider, _ := item["provider"].(string)
		fingerprint, _ := item["fingerprint"].(string)
		path, _ := item["relativePath"].(string)
		provenance, _ := item["provenance"].(string)
		if eventID == "" || provider == "" || provenance == "workflow_generated" || strings.HasPrefix(strings.TrimPrefix(path, "./"), ".summaries/") {
			continue
		}
		ok, err := s.database.ClaimWorkflowEvent(ctx, run.AgentInstanceID, run.WorkflowVersionID, provider, eventID, fingerprint, run.ID)
		if err == nil && !ok && run.TriggerKind == "retry" {
			ok, err = s.database.ReclaimFailedWorkflowEvent(ctx, run.AgentInstanceID, run.WorkflowVersionID, provider, eventID, fingerprint, run.ID)
		}
		if err != nil {
			return nil, err
		}
		if ok {
			item["claimedByRunId"] = run.ID
			claimed = append(claimed, item)
		}
	}
	return TestingMustAPIRawJSON(map[string]any{"items": claimed, "claimed": len(claimed), "provenance": map[string]any{"instanceId": run.AgentInstanceID, "workflowVersionId": run.WorkflowVersionID}}), nil
}
