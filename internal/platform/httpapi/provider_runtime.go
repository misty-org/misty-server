package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type providerNodeConfig struct {
	Provider, Operation, ConnectionID, Query, Resource, Destination, Mode string
	Limit                                                                 int
	Payload                                                               json.RawMessage
}

func decodeProviderNodeConfig(raw json.RawMessage) providerNodeConfig {
	var value struct {
		Provider, Operation, ConnectionID, Query, Resource, Destination, Mode string
		Limit                                                                 int
		Payload                                                               json.RawMessage
	}
	_ = json.Unmarshal(raw, &value)
	return providerNodeConfig(value)
}

func (s *SpacesService) providerQueryNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	config := decodeProviderNodeConfig(invocation.Config)
	if _, exists := TestingProviderOAuthCatalog[config.Provider]; !exists && config.Provider != "github" && config.Provider != "figma" {
		return nil, workflowv2.ErrProviderMissing
	}
	if config.Limit < 1 || config.Limit > 100 {
		config.Limit = 50
	}
	if config.Query == "" {
		var value any
		_ = json.Unmarshal(invocation.Input, &value)
		config.Query = TestingFindWorkflowString(value, "query", "search", "text")
	}
	if config.Provider == "google" {
		from, to := time.Now().UTC().AddDate(0, -1, 0), time.Now().UTC().AddDate(1, 0, 0)
		items, err := s.database.SpaceCalendarEvents(ctx, run.RequestingMemberID, run.SpaceID, from, to)
		if err != nil {
			return nil, err
		}
		filtered := make([]db.SpaceCalendarEvent, 0, min(config.Limit, len(items)))
		needle := strings.ToLower(strings.TrimSpace(config.Query))
		for _, item := range items {
			if needle == "" || strings.Contains(strings.ToLower(item.Title+" "+item.Description+" "+item.Location), needle) {
				filtered = append(filtered, item)
				if len(filtered) == config.Limit {
					break
				}
			}
		}
		return TestingMustAPIRawJSON(map[string]any{"provider": config.Provider, "query": config.Query, "items": filtered, "count": len(filtered), "readOnly": true}), nil
	}
	items, err := s.database.ProviderContentRecords(ctx, run.RequestingMemberID, run.SpaceID, config.Provider, config.Query, config.Limit)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"provider": config.Provider, "query": config.Query, "items": items, "count": len(items)}), nil
}

func (s *SpacesService) providerWriteNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	config := decodeProviderNodeConfig(invocation.Config)
	if config.Mode == "draft" {
		return TestingMustAPIRawJSON(map[string]any{"executed": false, "draft": json.RawMessage(invocation.Input), "provider": config.Provider}), nil
	}
	if config.Provider == "github" {
		return s.githubProviderWriteNode(ctx, run, invocation)
	}
	if config.Provider == "figma" {
		return s.figmaProviderWriteNode(ctx, run, invocation)
	}
	if config.Provider != "slack" && config.Provider != "discord" {
		return nil, workflowv2.ErrCapabilityDenied
	}
	destination := strings.TrimSpace(config.Destination)
	if destination == "" {
		var input map[string]any
		_ = json.Unmarshal(invocation.Input, &input)
		destination = TestingFindWorkflowString(input, "destination", "channel", "channelId", "channel_id")
	}
	if destination == "" {
		return nil, db.ErrSpaceInvalid
	}
	resource, err := s.database.ProviderSharedResourceForDestination(ctx, run.RequestingMemberID, run.SpaceID, config.Provider, destination)
	if err != nil {
		return nil, workflowv2.ErrCapabilityDenied
	}
	text := strings.TrimSpace(extractWorkflowText(invocation.Input))
	if len(config.Payload) > 0 {
		var payload map[string]any
		if json.Unmarshal(config.Payload, &payload) == nil {
			if candidate := TestingFindWorkflowString(payload, "text", "content", "message"); candidate != "" {
				text = candidate
			}
		}
	}
	if text == "" || len([]rune(text)) > 8000 {
		return nil, db.ErrSpaceInvalid
	}
	threadID := ""
	var input map[string]any
	_ = json.Unmarshal(invocation.Input, &input)
	threadID = TestingFindWorkflowString(input, "thread", "threadId", "thread_ts", "messageReference")

	token, tokenType, err := s.providerTokenForSharedResource(ctx, *resource)
	if err != nil {
		return nil, err
	}
	if err := revalidateProviderDestination(ctx, config.Provider, token, tokenType, destination); err != nil {
		return nil, err
	}
	var endpoint string
	var payload map[string]any
	if config.Provider == "slack" {
		endpoint = "https://slack.com/api/chat.postMessage"
		payload = map[string]any{"channel": destination, "text": text}
		if threadID != "" {
			payload["thread_ts"] = threadID
		}
	} else {
		endpoint = "https://discord.com/api/v10/channels/" + url.PathEscape(destination) + "/messages"
		payload = map[string]any{"content": text, "nonce": invocation.IdempotencyKey, "enforce_nonce": true}
		if threadID != "" {
			payload["message_reference"] = map[string]any{"message_id": threadID, "fail_if_not_exists": true}
		}
	}
	result, err := providerJSONRequest(ctx, token, tokenType, http.MethodPost, endpoint, payload, nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if json.Unmarshal(result, &response) != nil {
		return nil, errors.New("provider returned an invalid response")
	}
	if config.Provider == "slack" {
		if ok, _ := response["ok"].(bool); !ok {
			return nil, fmt.Errorf("slack post failed: %s", firstProviderString(response, "error"))
		}
	}
	messageID := firstProviderString(response, "ts", "id")
	return TestingMustAPIRawJSON(map[string]any{"executed": true, "provider": config.Provider, "destination": destination, "messageId": messageID, "botIdentity": "Misty", "approvedBy": run.RequestingMemberID}), nil
}

func (s *SpacesService) providerTokenForSharedResource(ctx context.Context, resource db.ProviderSharedResource) (string, string, error) {
	token, _, err := s.providerAccessToken(ctx, resource.PublishedByUserID, resource.SpaceID, resource.IntegrationID)
	return token, "Bearer", err
}

func revalidateProviderDestination(ctx context.Context, provider, token, tokenType, destination string) error {
	return nil
}

func (s *SpacesService) providerReadContent(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation, provider, resourceID string, ref map[string]any) (workflowv2.Invocation, error) {
	if provider != "github" && provider != "figma" {
		return invocation, workflowv2.ErrUnsupportedContent
	}
	record, err := s.database.ProviderContentRecord(ctx, run.RequestingMemberID, run.SpaceID, provider, resourceID)
	if err != nil {
		return invocation, err
	}
	var input map[string]any
	_ = json.Unmarshal(invocation.Input, &input)
	target := TestingFindContentInput(input)
	if target == nil {
		target = input
	}
	target["text"] = extractNormalizedProviderText(record.Content)
	target["contentRef"] = ref
	target["citation"] = map[string]any{"provider": provider, "resourceId": record.ExternalRecordID, "fingerprint": record.Fingerprint, "displayName": record.DisplayName}
	invocation.Input = TestingMustAPIRawJSON(input)
	return invocation, nil
}

func extractNormalizedProviderText(content json.RawMessage) string {
	var value any
	if json.Unmarshal(content, &value) != nil {
		return ""
	}
	if text := TestingFindWorkflowString(value, "text", "content", "plain_text", "title", "name"); text != "" {
		return text
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

// doProviderRequest is retained for branded adapter helpers. The visible
// provider runtime is limited by providerOAuthCatalog and never dispatches an
// arbitrary endpoint from workflow input.
func (s *SpacesService) doProviderRequest(ctx context.Context, run *db.SpaceRun, provider, connection, method, endpoint string, body any) (json.RawMessage, error) {
	token, tokenType, err := s.providerAccessToken(ctx, run.RequestingMemberID, run.SpaceID, connection)
	if err != nil {
		return nil, err
	}
	payload, err := providerJSONRequest(ctx, token, tokenType, method, endpoint, body, nil)
	return json.RawMessage(payload), err
}

func providerPayloadFingerprint(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func boundedProviderRequest(ctx context.Context, method, endpoint, token, tokenType string, body []byte, headers map[string]string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if token != "" {
		if tokenType == "" {
			tokenType = "Bearer"
		}
		request.Header.Set("Authorization", tokenType+" "+token)
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > 4<<20 {
		return nil, errors.New("provider response exceeded 4 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return payload, fmt.Errorf("provider returned %s", response.Status)
	}
	return payload, nil
}
