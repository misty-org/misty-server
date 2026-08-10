package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type availableProviderResource struct {
	Provider           string          `json:"provider"`
	ResourceType       string          `json:"resource_type"`
	ExternalResourceID string          `json:"external_resource_id"`
	DisplayName        string          `json:"display_name"`
	Configuration      json.RawMessage `json:"configuration"`
}

func (s *SpacesService) AvailableProviderResources() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, integrationID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "integrationID")
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		discovered, err := s.discoverProviderResources(r.Context(), userID, spaceID, integrationID)
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"resources": discovered})
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Resources []struct {
				ResourceType       string `json:"resource_type"`
				ExternalResourceID string `json:"external_resource_id"`
			} `json:"resources"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		desired := make([]db.ProviderSharedResource, 0, len(body.Resources))
		seen := map[string]bool{}
		for _, requested := range body.Resources {
			key := requested.ResourceType + "\x00" + requested.ExternalResourceID
			if seen[key] {
				continue
			}
			seen[key] = true
			var selected *availableProviderResource
			for index := range discovered {
				if discovered[index].ResourceType == requested.ResourceType &&
					discovered[index].ExternalResourceID == requested.ExternalResourceID {
					selected = &discovered[index]
					break
				}
			}
			if selected == nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			desired = append(desired, db.ProviderSharedResource{
				Provider: selected.Provider, ResourceType: selected.ResourceType,
				ExternalResourceID: selected.ExternalResourceID, DisplayName: selected.DisplayName,
				Configuration: selected.Configuration,
			})
		}
		items, err := s.database.ReplaceProviderSharedResources(
			r.Context(), userID, spaceID, integrationID, desired,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		for _, item := range items {
			resource := item
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if backfillErr := s.backfillProviderResource(ctx, resource); backfillErr != nil {
					_ = s.database.SetProviderSharedResourceHealth(
						ctx, resource.ID, "needs_attention", providerErrorCode(backfillErr),
					)
				}
			}()
		}
		if len(discovered) > 0 {
			_ = s.database.SetSpaceSetupProviderStatus(
				r.Context(), userID, spaceID, discovered[0].Provider, "configured",
			)
		}
		writeJSON(w, http.StatusOK, map[string]any{"resources": items})
	}
}

func (s *SpacesService) ProviderSharedResources() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.ProviderSharedResources(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"resources": items})
		case http.MethodPost:
			var body struct {
				IntegrationID      string          `json:"integration_id"`
				Provider           string          `json:"provider"`
				ResourceType       string          `json:"resource_type"`
				ExternalResourceID string          `json:"external_resource_id"`
				DisplayName        string          `json:"display_name"`
				Configuration      json.RawMessage `json:"configuration"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			discovered, err := s.discoverProviderResources(r.Context(), userID, spaceID, body.IntegrationID)
			if err != nil {
				writeProviderFailure(w, err)
				return
			}
			var selected *availableProviderResource
			for index := range discovered {
				if discovered[index].Provider == body.Provider && discovered[index].ResourceType == body.ResourceType && discovered[index].ExternalResourceID == body.ExternalResourceID {
					selected = &discovered[index]
					break
				}
			}
			if selected == nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			item, err := s.database.PublishProviderSharedResource(r.Context(), userID, db.ProviderSharedResource{SpaceID: spaceID, IntegrationID: body.IntegrationID, Provider: selected.Provider, ResourceType: selected.ResourceType, ExternalResourceID: selected.ExternalResourceID, DisplayName: selected.DisplayName, Configuration: selected.Configuration})
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			go func(resource db.ProviderSharedResource) {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if backfillErr := s.backfillProviderResource(ctx, resource); backfillErr != nil {
					_ = s.database.SetProviderSharedResourceHealth(ctx, resource.ID, "needs_attention", providerErrorCode(backfillErr))
				}
			}(*item)
			_ = s.database.SetSpaceSetupProviderStatus(
				r.Context(), userID, spaceID, selected.Provider, "configured",
			)
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) backfillProviderResource(ctx context.Context, resource db.ProviderSharedResource) error {
	token, tokenType, err := s.providerTokenForSharedResource(ctx, resource)
	if err != nil {
		return err
	}
	switch resource.Provider {
	case "slack":
		query := url.Values{"channel": {resource.ExternalResourceID}, "limit": {"100"}, "inclusive": {"true"}}
		payload, err := providerJSONRequest(ctx, token, tokenType, http.MethodGet, "https://slack.com/api/conversations.history?"+query.Encode(), nil, nil)
		if err != nil {
			return err
		}
		var page struct {
			OK       bool `json:"ok"`
			Messages []struct {
				TS       string          `json:"ts"`
				ThreadTS string          `json:"thread_ts"`
				User     string          `json:"user"`
				BotID    string          `json:"bot_id"`
				Text     string          `json:"text"`
				Files    json.RawMessage `json:"files"`
			} `json:"messages"`
		}
		if json.Unmarshal(payload, &page) != nil || !page.OK {
			return errors.New("slack history backfill failed")
		}
		for _, message := range page.Messages {
			content, _ := json.Marshal(map[string]any{"channel": resource.ExternalResourceID, "ts": message.TS, "thread_ts": message.ThreadTS, "user": message.User, "bot_id": message.BotID, "text": message.Text, "files": json.RawMessage(message.Files)})
			if err := s.database.UpsertProviderContentRecord(ctx, db.ProviderContentRecord{SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "slack", ExternalRecordID: message.TS, ParentExternalID: message.ThreadTS, RecordType: "message", Fingerprint: providerPayloadFingerprint(content), DisplayName: resource.DisplayName + " · " + message.User, MIMEType: "application/vnd.slack.message+json", OccurredAt: slackTimestamp(message.TS), Content: content}); err != nil {
				return err
			}
		}
	case "discord":
		payload, err := providerJSONRequest(ctx, token, "Bot", http.MethodGet, "https://discord.com/api/v10/channels/"+url.PathEscape(resource.ExternalResourceID)+"/messages?limit=100", nil, nil)
		if err != nil {
			return err
		}
		var messages []map[string]any
		if json.Unmarshal(payload, &messages) != nil {
			return errors.New("discord history backfill failed")
		}
		for _, message := range messages {
			externalID, _ := message["id"].(string)
			if externalID == "" {
				continue
			}
			content, _ := json.Marshal(message)
			display := resource.DisplayName
			if author, _ := message["author"].(map[string]any); author != nil {
				if username, _ := author["username"].(string); username != "" {
					display += " · " + username
				}
			}
			var occurredAt *time.Time
			if timestamp, _ := message["timestamp"].(string); timestamp != "" {
				if parsed, parseErr := time.Parse(time.RFC3339Nano, timestamp); parseErr == nil {
					occurredAt = &parsed
				}
			}
			if err := s.database.UpsertProviderContentRecord(ctx, db.ProviderContentRecord{SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "discord", ExternalRecordID: externalID, RecordType: "message", Fingerprint: providerPayloadFingerprint(content), DisplayName: display, MIMEType: "application/vnd.discord.message+json", OccurredAt: occurredAt, Content: content}); err != nil {
				return err
			}
		}
	case "notion":
		event := notionWebhookEvent{ID: "initial:" + resource.ID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Type: resource.ResourceType + ".initial_sync"}
		event.Entity.ID, event.Entity.Type = resource.ExternalResourceID, resource.ResourceType
		if event.Entity.Type == "data_source" || event.Entity.Type == "database" || event.Entity.Type == "page" {
			if err := s.fetchAndStoreNotionEntity(ctx, resource, event, TestingMustAPIRawJSON(event)); err != nil {
				return err
			}
			break
		}
		return db.ErrSpaceInvalid
	}
	return s.database.SetProviderSharedResourceHealth(ctx, resource.ID, "active", "")
}
