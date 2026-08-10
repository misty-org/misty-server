package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Notion read/write proxy.
//
// The Notion token never leaves the server: the desktop client asks Misty for
// Notion data and Misty makes the upstream call with the Space's stored
// credential. Every route is Space-scoped because that is where the credential
// and its permissions live.

const notionVersion = "2026-03-11"

// notionRequest performs one upstream call with the credential belonging to an
// explicitly selected Space resource. Members never receive the credential or
// gain a path to unselected workspace content.
func (s *SpacesService) notionRequest(
	ctx context.Context,
	resource db.ProviderSharedResource,
	method, endpoint string,
	body any,
) ([]byte, error) {
	token, tokenType, err := s.providerTokenForSharedResource(ctx, resource)
	if err != nil {
		return nil, err
	}
	return providerJSONRequest(ctx, token, tokenType, method, endpoint, body, map[string]string{"Notion-Version": notionVersion})
}

func (s *SpacesService) notionResource(
	ctx context.Context,
	userID, spaceID, externalResourceID string,
) (*db.ProviderSharedResource, error) {
	return s.database.ProviderSharedResourceForNotionEntity(
		ctx, userID, spaceID, strings.TrimSpace(externalResourceID),
	)
}

func (s *SpacesService) notionIntegrationID(ctx context.Context, userID, spaceID string) (string, error) {
	integrations, err := s.database.SpaceIntegrations(ctx, userID, spaceID)
	if err != nil {
		return "", err
	}
	for _, integration := range integrations {
		if integration.Provider == "notion" && integration.Status != "revoked" {
			return integration.ID, nil
		}
	}
	return "", db.ErrSpaceInvalid
}

// NotionStatus reports whether this Space can reach Notion. A Space with no
// Notion connection is the normal case, not an error, so the Notes pane can
// render its other sources without a failure state.
func (s *SpacesService) NotionStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		resources, err := s.database.ProviderSharedResources(r.Context(), userID, spaceID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"connected": false})
			return
		}
		for _, resource := range resources {
			if resource.Provider == "notion" && resource.Status == "active" {
				writeJSON(w, http.StatusOK, map[string]any{
					"connected": true, "integration_id": resource.IntegrationID,
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
	}
}

// NotionConnection removes the Space's Notion connection.
func (s *SpacesService) NotionConnection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		integrationID, err := s.notionIntegrationID(r.Context(), userID, spaceID)
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := s.database.DeleteProviderIntegration(r.Context(), userID, integrationID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type notionSourceOption struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	ParentTitle string `json:"parentTitle,omitempty"`
}

// NotionSources lists only sources already selected by the Space owner.
// Discovery lives in integration management and is owner-only.
func (s *SpacesService) NotionSources() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		resources, err := s.database.ProviderSharedResources(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		items := []notionSourceOption{}
		for _, resource := range resources {
			if resource.Provider != "notion" || resource.Status != "active" {
				continue
			}
			kind := resource.ResourceType
			if kind == "data_source" {
				kind = "database"
			}
			items = append(items, notionSourceOption{
				ID: resource.ExternalResourceID, Kind: kind, Title: resource.DisplayName,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": items})
	}
}

func notionSourceFromObject(raw json.RawMessage) (notionSourceOption, bool) {
	var object struct {
		ID       string          `json:"id"`
		Object   string          `json:"object"`
		URL      string          `json:"url"`
		Archived bool            `json:"archived"`
		Title    json.RawMessage `json:"title"`
		Parent   struct {
			Type string `json:"type"`
		} `json:"parent"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(raw, &object) != nil || object.ID == "" || object.Archived {
		return notionSourceOption{}, false
	}
	kind := "page"
	if object.Object == "database" || object.Object == "data_source" {
		kind = "database"
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	title := notionObjectTitle(decoded)
	if strings.TrimSpace(title) == "" {
		title = "Untitled"
	}
	return notionSourceOption{ID: object.ID, Kind: kind, Title: title, URL: object.URL, ParentTitle: object.Parent.Type}, true
}

// NotionPage proxies a single page read and its property patch.
func (s *SpacesService) NotionPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, pageID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "pageID")
		resource, err := s.notionResource(r.Context(), userID, spaceID, pageID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		endpoint := "https://api.notion.com/v1/pages/" + url.PathEscape(pageID)
		switch r.Method {
		case http.MethodGet:
			s.proxyNotion(w, r, *resource, http.MethodGet, endpoint, nil)
		case http.MethodPatch:
			var body struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if len(body.Properties) == 0 {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			s.proxyNotion(w, r, *resource, http.MethodPatch, endpoint, map[string]any{"properties": body.Properties})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
