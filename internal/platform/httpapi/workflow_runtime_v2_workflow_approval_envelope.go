package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func TestingWorkflowApprovalEnvelope(run *db.SpaceRun, actionKind, provider, connectionID, destination string, input json.RawMessage) json.RawMessage {
	var decoded any
	_ = json.Unmarshal(input, &decoded)
	reason := TestingFindWorkflowString(decoded, "reason", "rationale", "completionCriteria", "completion_criteria")
	if reason == "" {
		reason = "The Agent needs this action to continue the pinned workflow run."
	}
	return TestingMustAPIRawJSON(map[string]any{
		"agent_id":            run.AgentID,
		"agent_version_id":    run.AgentVersionID,
		"workflow_version_id": run.WorkflowVersionID,
		"run_id":              run.ID,
		"action_kind":         actionKind,
		"provider":            provider,
		"connection_id":       connectionID,
		"destination":         destination,
		"bot_identity":        map[string]string{"name": "Misty", "provider": provider},
		"content_preview":     extractWorkflowText(input),
		"reason":              reason,
		"affected_resources":  []string{destination},
		"citations":           findWorkflowValue(decoded, "citations"),
		"input":               json.RawMessage(input),
		"reversibility":       "Provider actions may not be reversible after execution.",
	})
}

func findWorkflowValue(value any, key string) any {
	switch item := value.(type) {
	case map[string]any:
		if found, ok := item[key]; ok {
			return found
		}
		for _, child := range item {
			if found := findWorkflowValue(child, key); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range item {
			if found := findWorkflowValue(child, key); found != nil {
				return found
			}
		}
	}
	return nil
}

func (s *SpacesService) prepareContentInvocation(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (workflowv2.Invocation, error) {
	var input map[string]any
	if json.Unmarshal(invocation.Input, &input) != nil {
		return invocation, workflowv2.ErrOutputInvalid
	}
	target := TestingFindContentInput(input)
	if target == nil {
		target = input
	}
	if _, hasText := target["text"]; hasText {
		return invocation, nil
	}
	refValue, _ := target["contentRef"].(map[string]any)
	if refValue == nil {
		refValue, _ = target["content"].(map[string]any)
	}
	if refValue == nil {
		return invocation, nil
	}
	sourceKind, _ := refValue["sourceKind"].(string)
	providerID, _ := refValue["providerId"].(string)
	resourceID, _ := refValue["resourceId"].(string)
	if sourceKind == "local_file" || providerID == "device" {
		return invocation, workflowv2.ErrDeviceUnavailable
	}
	if sourceKind != "library" && providerID != "library" {
		return s.providerReadContent(ctx, run, invocation, providerID, resourceID, refValue)
	}
	if s.library == nil || resourceID == "" {
		return invocation, workflowv2.ErrProviderMissing
	}
	var config struct {
		MaximumBytes int64 `json:"maximumBytes"`
	}
	_ = json.Unmarshal(invocation.Config, &config)
	data, download, err := s.library.ReadTextItem(ctx, run.RequestingMemberID, run.SpaceID, resourceID, config.MaximumBytes)
	if err != nil {
		return invocation, err
	}
	target["text"] = string(data)
	refValue["displayName"] = download.Filename
	refValue["mimeType"] = download.MIMEType
	refValue["fingerprint"] = download.SHA256
	refValue["version"] = download.SHA256
	target["contentRef"] = refValue
	invocation.Input = TestingMustAPIRawJSON(input)
	return invocation, nil
}

func TestingFindContentInput(value any) map[string]any {
	switch item := value.(type) {
	case map[string]any:
		if _, ok := item["contentRef"]; ok {
			return item
		}
		for _, child := range item {
			if found := TestingFindContentInput(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range item {
			if found := TestingFindContentInput(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func (s *SpacesService) sourceQueryNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var config struct {
		Source string `json:"source"`
		Query  string `json:"query"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(invocation.Config, &config)
	if config.Limit < 1 || config.Limit > 100 {
		config.Limit = 50
	}
	if config.Query == "" {
		var value any
		_ = json.Unmarshal(invocation.Input, &value)
		config.Query = TestingFindWorkflowString(value, "query", "search", "text")
	}
	if config.Source == "" {
		config.Source = "all"
	}
	results := make([]any, 0, config.Limit)
	if config.Source == "all" || config.Source == "library" {
		items, err := s.database.LibraryItems(ctx, run.RequestingMemberID, run.SpaceID, db.LibraryItemQuery{Search: config.Query, Limit: config.Limit, Visibility: "visible"})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			results = append(results, map[string]any{
				"contentRef": map[string]any{"sourceKind": "library", "providerId": "library", "resourceId": item.ID, "version": strconv.FormatInt(item.Version, 10), "displayName": item.DisplayName, "permissionScope": "space:" + run.SpaceID},
				"metadata":   map[string]any{"caption": item.Caption, "tags": item.Tags, "updatedAt": item.UpdatedAt},
			})
			if len(results) == config.Limit {
				break
			}
		}
	}
	if len(results) < config.Limit && (config.Source == "all" || config.Source == "messages") {
		messages, err := s.database.SpaceMessages(ctx, run.RequestingMemberID, run.SpaceID, 0, 100)
		if err != nil {
			return nil, err
		}
		query := strings.ToLower(strings.TrimSpace(config.Query))
		for _, message := range messages {
			raw, _ := json.Marshal(message.Content)
			if query != "" && !strings.Contains(strings.ToLower(string(raw)), query) {
				continue
			}
			results = append(results, map[string]any{
				"contentRef": map[string]any{"sourceKind": "message", "providerId": "space_chat", "resourceId": message.ID, "version": strconv.FormatInt(message.Seq, 10), "displayName": "Message from " + message.SenderName, "permissionScope": "space:" + run.SpaceID},
				"text":       string(raw),
			})
			if len(results) == config.Limit {
				break
			}
		}
	}
	return TestingMustAPIRawJSON(map[string]any{"items": results, "count": len(results), "query": config.Query, "source": config.Source}), nil
}

func (s *SpacesService) readMetadataNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var value any
	if json.Unmarshal(invocation.Input, &value) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	provider := TestingFindWorkflowString(value, "providerId")
	resourceID := TestingFindWorkflowString(value, "resourceId", "itemId")
	if provider != "library" || resourceID == "" {
		return TestingMustAPIRawJSON(map[string]any{"metadata": value}), nil
	}
	item, err := s.database.LibraryItem(ctx, run.RequestingMemberID, run.SpaceID, resourceID)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"contentRef": map[string]any{"sourceKind": "library", "providerId": "library", "resourceId": item.ID, "version": strconv.FormatInt(item.Version, 10), "displayName": item.DisplayName, "permissionScope": "space:" + run.SpaceID}, "metadata": map[string]any{"caption": item.Caption, "tags": item.Tags, "favorite": item.Favorite, "hidden": item.Hidden, "updatedAt": item.UpdatedAt}}), nil
}

func (s *SpacesService) updateMetadataNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var value any
	if json.Unmarshal(invocation.Input, &value) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	itemID := TestingFindWorkflowString(value, "resourceId", "itemId")
	if itemID == "" {
		return nil, workflowv2.ErrOutputInvalid
	}
	item, err := s.database.LibraryItem(ctx, run.RequestingMemberID, run.SpaceID, itemID)
	if err != nil {
		return nil, err
	}
	tags := findWorkflowStrings(value, "tags")
	merged := append([]string{}, item.Tags...)
	seen := map[string]bool{}
	for _, tag := range merged {
		seen[strings.ToLower(tag)] = true
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[strings.ToLower(tag)] {
			merged = append(merged, tag)
			seen[strings.ToLower(tag)] = true
		}
	}
	caption := TestingFindWorkflowString(value, "caption", "summary")
	if caption == "" {
		caption = item.Caption
	}
	updated, err := s.database.UpdateLibraryItem(ctx, run.RequestingMemberID, run.SpaceID, item.ID, item.Version, item.DisplayName, caption, merged, item.Favorite, item.Hidden)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"updated": true, "itemId": updated.ID, "version": updated.Version, "tags": updated.Tags}), nil
}
