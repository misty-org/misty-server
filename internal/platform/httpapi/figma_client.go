package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

type FigmaProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type FigmaFileSummary struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

type FigmaVersion struct {
	ID          string         `json:"id"`
	CreatedAt   string         `json:"created_at"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	User        map[string]any `json:"user"`
}

type FigmaComment struct {
	ID         string         `json:"id"`
	Message    string         `json:"message"`
	CreatedAt  string         `json:"created_at"`
	ResolvedAt *string        `json:"resolved_at"`
	User       map[string]any `json:"user"`
	ClientMeta map[string]any `json:"client_meta"`
}

type FigmaFileContext struct {
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	LastModified string          `json:"last_modified"`
	EditorType   string          `json:"editor_type"`
	ThumbnailURL string          `json:"thumbnail_url"`
	Document     json.RawMessage `json:"document"`
}

type FigmaWebhook struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Context   string `json:"context"`
	ContextID string `json:"context_id"`
	Status    string `json:"status"`
}

type FigmaProvider interface {
	Projects(context.Context, string) ([]FigmaProject, error)
	ProjectFiles(context.Context, string) ([]FigmaFileSummary, error)
	File(context.Context, string) (FigmaFileContext, error)
	Versions(context.Context, string) ([]FigmaVersion, error)
	Comments(context.Context, string) ([]FigmaComment, error)
	PostComment(context.Context, string, string, string) (FigmaComment, error)
	CreateWebhook(context.Context, string, string, string, string, string) (FigmaWebhook, error)
	DeleteWebhook(context.Context, string) error
}
type FigmaProviderFactory func(string) FigmaProvider
type figmaClient struct {
	token, base string
	client      *http.Client
}
type figmaAPIError struct {
	Status     int
	RetryAfter string
}

func (e *figmaAPIError) Error() string { return fmt.Sprintf("figma_api_%d", e.Status) }

func newFigmaClient(token string) *figmaClient {
	base := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("FIGMA_API_BASE_URL")), "/")
	if base == "" {
		base = "https://api.figma.com"
	}
	return &figmaClient{token: token, base: base, client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *figmaClient) request(ctx context.Context, method, path string, body any, limit int64, out any) error {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return errors.New("figma_response_too_large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &figmaAPIError{Status: response.StatusCode, RetryAfter: strings.TrimSpace(response.Header.Get("Retry-After"))}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func (c *figmaClient) Projects(ctx context.Context, teamID string) ([]FigmaProject, error) {
	var out struct {
		Projects []FigmaProject `json:"projects"`
	}
	err := c.request(ctx, http.MethodGet, "/v1/teams/"+url.PathEscape(teamID)+"/projects", nil, 1<<20, &out)
	return out.Projects, err
}
func (c *figmaClient) ProjectFiles(ctx context.Context, projectID string) ([]FigmaFileSummary, error) {
	var out struct {
		Files []FigmaFileSummary `json:"files"`
	}
	err := c.request(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(projectID)+"/files?branch_data=false", nil, 2<<20, &out)
	return out.Files, err
}
func (c *figmaClient) File(ctx context.Context, key string) (FigmaFileContext, error) {
	var raw struct {
		Name, Version, LastModified, EditorType, ThumbnailURL string
		Document                                              json.RawMessage
	}
	err := c.request(ctx, http.MethodGet, "/v1/files/"+url.PathEscape(key)+"?depth=2&branch_data=true", nil, 8<<20, &raw)
	return FigmaFileContext{Key: key, Name: raw.Name, Version: raw.Version, LastModified: raw.LastModified, EditorType: raw.EditorType, ThumbnailURL: raw.ThumbnailURL, Document: raw.Document}, err
}
func (c *figmaClient) Versions(ctx context.Context, key string) ([]FigmaVersion, error) {
	var out struct {
		Versions []FigmaVersion `json:"versions"`
	}
	err := c.request(ctx, http.MethodGet, "/v1/files/"+url.PathEscape(key)+"/versions?page_size=100", nil, 2<<20, &out)
	return out.Versions, err
}
func (c *figmaClient) Comments(ctx context.Context, key string) ([]FigmaComment, error) {
	var out struct {
		Comments []FigmaComment `json:"comments"`
	}
	err := c.request(ctx, http.MethodGet, "/v1/files/"+url.PathEscape(key)+"/comments", nil, 2<<20, &out)
	return out.Comments, err
}
func (c *figmaClient) PostComment(ctx context.Context, key, message, nodeID string) (FigmaComment, error) {
	body := map[string]any{"message": message}
	if nodeID != "" {
		body["client_meta"] = map[string]any{"node_id": nodeID}
	}
	var out FigmaComment
	err := c.request(ctx, http.MethodPost, "/v1/files/"+url.PathEscape(key)+"/comments", body, 1<<20, &out)
	return out, err
}
func (c *figmaClient) CreateWebhook(ctx context.Context, eventType, contextKind, contextID, endpoint, passcode string) (FigmaWebhook, error) {
	var out FigmaWebhook
	err := c.request(ctx, http.MethodPost, "/v2/webhooks", map[string]any{"event_type": eventType, "context": contextKind, "context_id": contextID, "endpoint": endpoint, "passcode": passcode, "description": "Misty Drawings sync"}, 1<<20, &out)
	return out, err
}
func (c *figmaClient) DeleteWebhook(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/v2/webhooks/"+url.PathEscape(id), nil, 1<<20, nil)
}
