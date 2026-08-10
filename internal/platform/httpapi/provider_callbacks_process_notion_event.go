package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) processNotionEvent(ctx context.Context, event notionWebhookEvent, raw []byte) error {
	resourceID := event.Entity.ID
	resources, err := s.database.MatchingProviderResources(ctx, "notion", event.WorkspaceID, resourceID)
	if err != nil {
		return err
	}
	if len(resources) == 0 && event.Data.Parent.ID != "" {
		resources, err = s.database.MatchingProviderResources(ctx, "notion", event.WorkspaceID, event.Data.Parent.ID)
	}
	if err != nil {
		return err
	}
	for _, resource := range resources {
		claimed, claimErr := s.database.EnqueueProviderEvent(ctx, resource, event.ID, raw)
		if claimErr != nil || !claimed {
			continue
		}
		state := "processed"
		if err := s.fetchAndStoreNotionEntity(ctx, resource, event, raw); err != nil {
			state = "failed"
		} else {
			_, _ = s.ProcessProviderEvent(ctx, resource, event.ID, providerPayloadFingerprint(raw), json.RawMessage(raw))
		}
		_ = s.database.FinishProviderEvent(ctx, resource.IntegrationID, event.ID, state)
	}
	return nil
}

func (s *SpacesService) fetchAndStoreNotionEntity(ctx context.Context, resource db.ProviderSharedResource, event notionWebhookEvent, raw []byte) error {
	deleted := strings.HasSuffix(event.Type, ".deleted")
	var deletedAt *time.Time
	content := json.RawMessage(raw)
	displayName := resource.DisplayName
	if deleted {
		now := time.Now().UTC()
		deletedAt = &now
	} else {
		token, tokenType, err := s.providerTokenForSharedResource(ctx, resource)
		if err != nil {
			return err
		}
		entityType := event.Entity.Type
		endpoint := "https://api.notion.com/v1/pages/" + url.PathEscape(event.Entity.ID)
		switch entityType {
		case "database":
			endpoint = "https://api.notion.com/v1/databases/" + url.PathEscape(event.Entity.ID)
		case "data_source":
			endpoint = "https://api.notion.com/v1/data_sources/" + url.PathEscape(event.Entity.ID)
		}
		object, requestErr := providerJSONRequest(ctx, token, tokenType, http.MethodGet, endpoint, nil, map[string]string{"Notion-Version": "2026-03-11"})
		if requestErr != nil {
			return requestErr
		}
		combined := map[string]any{"object": json.RawMessage(object), "event_type": event.Type}
		if entityType == "page" || entityType == "block" {
			blocks, blockErr := fetchNotionBlocks(ctx, token, tokenType, event.Entity.ID, 500)
			if blockErr != nil {
				return blockErr
			}
			combined["blocks"] = blocks
		}
		content, _ = json.Marshal(combined)
		var objectValue map[string]any
		_ = json.Unmarshal(object, &objectValue)
		if title := notionObjectTitle(objectValue); title != "" {
			displayName = title
		}
	}
	occurred, _ := time.Parse(time.RFC3339Nano, event.Timestamp)
	var occurredAt *time.Time
	if !occurred.IsZero() {
		occurredAt = &occurred
	}
	return s.database.UpsertProviderContentRecord(ctx, db.ProviderContentRecord{SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "notion", ExternalRecordID: event.Entity.ID, ParentExternalID: event.Data.Parent.ID, RecordType: event.Entity.Type, Fingerprint: providerPayloadFingerprint(content), DisplayName: displayName, MIMEType: "application/vnd.notion+json", OccurredAt: occurredAt, Content: content, DeletedAt: deletedAt})
}

func fetchNotionBlocks(ctx context.Context, token, tokenType, blockID string, maximum int) ([]any, error) {
	items := []any{}
	if maximum <= 0 {
		return items, nil
	}
	if err := appendNotionBlockChildren(ctx, token, tokenType, blockID, &items, maximum, 0); err != nil {
		return nil, err
	}
	return items, nil
}

// appendNotionBlockChildren preserves the parent-before-child citation order while
// bounding both total blocks and nesting depth. Notion currently limits block
// nesting, but the local depth guard prevents malformed provider data from turning
// one notification into unbounded work.
func appendNotionBlockChildren(ctx context.Context, token, tokenType, blockID string, items *[]any, maximum, depth int) error {
	if len(*items) >= maximum || depth >= 32 {
		return nil
	}
	cursor := ""
	for len(*items) < maximum {
		query := url.Values{"page_size": {"100"}}
		if cursor != "" {
			query.Set("start_cursor", cursor)
		}
		payload, err := providerJSONRequest(ctx, token, tokenType, http.MethodGet, "https://api.notion.com/v1/blocks/"+url.PathEscape(blockID)+"/children?"+query.Encode(), nil, map[string]string{"Notion-Version": "2026-03-11"})
		if err != nil {
			return err
		}
		var page struct {
			Results    []any  `json:"results"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if json.Unmarshal(payload, &page) != nil {
			return errors.New("notion blocks response was invalid")
		}
		for _, result := range page.Results {
			if len(*items) >= maximum {
				break
			}
			*items = append(*items, result)
			block, ok := result.(map[string]any)
			if !ok {
				continue
			}
			hasChildren, _ := block["has_children"].(bool)
			childID, _ := block["id"].(string)
			if hasChildren && childID != "" {
				if err := appendNotionBlockChildren(ctx, token, tokenType, childID, items, maximum, depth+1); err != nil {
					return err
				}
			}
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return nil
}

func readProviderCallbackBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, providerCallbackBodyLimit+1))
	if err != nil || len(raw) > providerCallbackBodyLimit {
		return nil, errors.New("provider callback body exceeded limit")
	}
	return raw, nil
}
