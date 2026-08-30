package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func writeProviderFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrSpaceForbidden) {
		writeSpaceError(w, err)
		return
	}
	var googleErr *googleAPIError
	var providerErr *providerAPIError
	if errors.As(err, &googleErr) || errors.As(err, &providerErr) {
		status := http.StatusBadGateway
		providerStatus := 0
		if googleErr != nil {
			providerStatus = googleErr.Status
		} else if providerErr != nil {
			providerStatus = providerErr.Status
		}
		if providerStatus == http.StatusUnauthorized || providerStatus == http.StatusForbidden {
			status = http.StatusFailedDependency
		} else if providerStatus == http.StatusPreconditionFailed {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"code": providerErrorCode(err)})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"code": "provider_error", "message": err.Error()})
}

func providerErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var googleErr *googleAPIError
	var providerErr *providerAPIError
	status := 0
	if errors.As(err, &googleErr) {
		status = googleErr.Status
	} else if errors.As(err, &providerErr) {
		status = providerErr.Status
	}
	switch status {
	case http.StatusUnauthorized:
		return "connection_revoked"
	case http.StatusForbidden:
		return "permission_missing"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusGone:
		return "cursor_expired"
	case http.StatusPreconditionFailed:
		return "conflict"
	}
	if errors.Is(err, db.ErrSpaceConflict) {
		return "conflict"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "provider_timeout"
	}
	return "provider_error"
}

func (s *SpacesService) backfillProviderResource(ctx context.Context, resource db.ProviderSharedResource) error {
	return s.database.SetProviderSharedResourceHealth(ctx, resource.ID, "active", "")
}
