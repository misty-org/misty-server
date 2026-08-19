package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type figmaBindingInput struct {
	ConnectionID string `json:"connection_id"`
	ResourceType string `json:"resource_type"`
	TeamID       string `json:"team_id"`
	ProjectID    string `json:"project_id"`
	FileKey      string `json:"file_key"`
	FileURL      string `json:"file_url"`
}

func (s *SpacesService) FigmaBindings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.FigmaBindings(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"bindings": items})
			return
		}

		var body figmaBindingInput
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), userID, body.ConnectionID, "drawings_read")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider := s.figmaProvider(token)
		item := db.FigmaBinding{ResourceType: strings.ToLower(strings.TrimSpace(body.ResourceType)), TeamID: strings.TrimSpace(body.TeamID)}
		switch item.ResourceType {
		case "file":
			fileKey, valid := figmaFileKey(body.FileKey, body.FileURL)
			if !valid {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			file, fetchErr := provider.File(r.Context(), fileKey)
			if fetchErr != nil {
				writeFigmaError(w, fetchErr)
				return
			}
			item.ExternalID = fileKey
			item.DisplayName = firstNonempty(file.Name, "Figma file")
		case "project":
			// Optional private-app path: Figma does not generally make project
			// list APIs available to public OAuth apps.
			item.ExternalID = strings.TrimSpace(body.ProjectID)
			if item.ExternalID == "" {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			files, fetchErr := provider.ProjectFiles(r.Context(), item.ExternalID)
			if fetchErr != nil {
				writeFigmaDiscoveryError(w, fetchErr)
				return
			}
			item.DisplayName = "Figma project " + item.ExternalID
			if item.TeamID != "" {
				if projects, projectErr := provider.Projects(r.Context(), item.TeamID); projectErr == nil {
					for _, project := range projects {
						if project.ID == item.ExternalID {
							item.DisplayName = project.Name
						}
					}
				}
			}
			_ = files
		default:
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}

		binding, err := s.database.CreateFigmaBinding(r.Context(), userID, spaceID, body.ConnectionID, item)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		count, syncErr := s.syncFigmaBinding(r.Context(), binding, provider)
		if syncErr != nil {
			_ = s.database.SetFigmaBindingSync(r.Context(), binding.ID, "", "needs_attention", "figma_sync_failed")
		}
		if account, _ := s.database.ConnectedAccount(r.Context(), userID, body.ConnectionID); figmaAccountHasCapability(account, "drawings_webhooks") {
			_, _ = s.reconcileFigmaWebhooks(r, binding, provider)
		}
		refreshed, _ := s.database.FigmaBinding(r.Context(), userID, spaceID, binding.ID)
		writeJSON(w, http.StatusCreated, map[string]any{"binding": refreshed, "records_synced": count})
	}
}

func (s *SpacesService) FigmaBinding() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, id := chi.URLParam(r, "spaceID"), chi.URLParam(r, "bindingID")
		binding, err := s.database.FigmaBinding(r.Context(), userID, spaceID, id)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		if token, _, tokenErr := s.connectedAccountAccessTokenForCapability(r.Context(), binding.BoundByUserID, binding.ConnectionID, "drawings_webhooks"); tokenErr == nil {
			provider := s.figmaProvider(token)
			subscriptions, _ := s.database.FigmaWebhookSubscriptions(r.Context(), binding.ID)
			for _, subscription := range subscriptions {
				_ = provider.DeleteWebhook(r.Context(), subscription.WebhookID)
			}
		}
		_ = s.database.DisableFigmaWebhookSubscriptions(r.Context(), binding.ID)
		if err := s.database.DisableFigmaBinding(r.Context(), userID, spaceID, id); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) SyncFigmaBinding() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		binding, err := s.database.FigmaBinding(r.Context(), userID, spaceID, chi.URLParam(r, "bindingID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), binding.BoundByUserID, binding.ConnectionID, "drawings_read")
		if err != nil {
			_ = s.database.SetFigmaBindingSync(r.Context(), binding.ID, binding.SyncCursor, "needs_attention", "reauthorization_required")
			writeSpaceError(w, err)
			return
		}
		count, err := s.syncFigmaBinding(r.Context(), binding, s.figmaProvider(token))
		if err != nil {
			_ = s.database.SetFigmaBindingSync(r.Context(), binding.ID, binding.SyncCursor, "needs_attention", "figma_sync_failed")
			writeFigmaError(w, err)
			return
		}
		refreshed, _ := s.database.FigmaBinding(r.Context(), userID, spaceID, binding.ID)
		writeJSON(w, http.StatusOK, map[string]any{"binding": refreshed, "records_synced": count})
	}
}

func (s *SpacesService) syncFigmaBinding(ctx context.Context, binding *db.FigmaBinding, provider FigmaProvider) (int, error) {
	var records []db.FigmaContentRecord
	if binding.ResourceType == "file" {
		file, err := provider.File(ctx, binding.FileKey)
		if err != nil {
			return 0, err
		}
		versions, err := provider.Versions(ctx, binding.FileKey)
		if err != nil {
			return 0, err
		}
		comments, err := provider.Comments(ctx, binding.FileKey)
		if err != nil {
			return 0, err
		}
		records = normalizeFigmaFileRecords(binding.ID, file, versions, comments)
	} else {
		files, err := provider.ProjectFiles(ctx, binding.ProjectID)
		if err != nil {
			return 0, err
		}
		if len(files) > 50 {
			files = files[:50]
		}
		records = normalizeFigmaProjectRecords(binding.ID, binding.ProjectID, files)
	}
	for index := range records {
		if err := s.database.UpsertFigmaContentRecord(ctx, records[index]); err != nil {
			return index, err
		}
	}
	cursor := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.database.SetFigmaBindingSync(ctx, binding.ID, cursor, "active", ""); err != nil {
		return len(records), err
	}
	return len(records), nil
}

func (s *SpacesService) ReconcileFigmaWebhooks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		binding, err := s.database.FigmaBinding(r.Context(), userID, spaceID, chi.URLParam(r, "bindingID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), binding.BoundByUserID, binding.ConnectionID, "drawings_webhooks")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		subscriptions, err := s.reconcileFigmaWebhooks(r, binding, s.figmaProvider(token))
		if err != nil {
			_ = s.database.SetFigmaBindingHealth(r.Context(), binding.ID, "needs_attention", "figma_webhook_setup_failed")
			writeFigmaError(w, err)
			return
		}
		_ = s.database.SetFigmaBindingHealth(r.Context(), binding.ID, "active", "")
		refreshed, _ := s.database.FigmaBinding(r.Context(), userID, spaceID, binding.ID)
		writeJSON(w, http.StatusOK, map[string]any{"binding": refreshed, "subscriptions": subscriptions})
	}
}

func (s *SpacesService) reconcileFigmaWebhooks(r *http.Request, binding *db.FigmaBinding, provider FigmaProvider) ([]db.FigmaWebhookSubscription, error) {
	existing, err := s.database.FigmaWebhookSubscriptions(r.Context(), binding.ID)
	if err != nil {
		return nil, err
	}
	byEvent := map[string]bool{}
	for _, subscription := range existing {
		byEvent[subscription.EventType] = true
	}
	endpoint := requestPublicAPIBase(r) + "/provider-callbacks/figma"
	for _, eventType := range []string{"FILE_UPDATE", "FILE_VERSION_UPDATE", "FILE_COMMENT"} {
		if byEvent[eventType] {
			continue
		}
		passcode := randomProviderValue(24)
		webhook, err := provider.CreateWebhook(r.Context(), eventType, binding.ResourceType, binding.ExternalID, endpoint, passcode)
		if err != nil {
			return nil, err
		}
		if _, err := s.database.SaveFigmaWebhookSubscription(r.Context(), binding.ID, webhook.ID, eventType, hashProviderValue(passcode)); err != nil {
			_ = provider.DeleteWebhook(r.Context(), webhook.ID)
			return nil, err
		}
	}
	return s.database.FigmaWebhookSubscriptions(r.Context(), binding.ID)
}

func figmaFileKey(key, rawURL string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" && strings.TrimSpace(rawURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(rawURL))
		host := strings.ToLower(parsed.Hostname())
		if err != nil || parsed.Scheme != "https" || (host != "figma.com" && !strings.HasSuffix(host, ".figma.com")) {
			return "", false
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for index, part := range parts {
			if (part == "file" || part == "design" || part == "board" || part == "proto") && index+1 < len(parts) {
				key = parts[index+1]
				break
			}
		}
	}
	if key == "" || len(key) > 256 || strings.ContainsAny(key, "/?#") {
		return "", false
	}
	return key, true
}
