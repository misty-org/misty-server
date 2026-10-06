package composio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,160}$`)
	slugPattern    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,127}$`)
	toolkitPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
)

func ValidToolSlug(slug string) bool { return slugPattern.MatchString(slug) }
func ValidToolkit(slug string) bool  { return toolkitPattern.MatchString(slug) }

// CreateSession opens an account's session: every toolkit, no remote sandbox,
// and connection prompts left to Misty's own Connect card.
func (c *Client) CreateSession(ctx context.Context, userID string) (string, error) {
	var out struct {
		SessionID string `json:"session_id"`
	}
	err := c.do(ctx, http.MethodPost, "tool_router/session", nil, map[string]any{
		"user_id":            userID,
		"manage_connections": map[string]any{"enable": false},
		"workbench":          map[string]any{"enable": false},
	}, &out)
	if err != nil {
		return "", err
	}
	if !sessionPattern.MatchString(out.SessionID) {
		return "", &APIError{Status: http.StatusBadGateway, Message: "Composio returned an invalid session."}
	}
	return out.SessionID, nil
}

func sessionPath(session, operation string) string {
	return "tool_router/session/" + url.PathEscape(session) + "/" + operation
}

// SearchResult keeps the parts of a session search Misty passes to the model.
type SearchResult struct {
	Success bool    `json:"success"`
	Error   *string `json:"error"`
	Results []struct {
		UseCase              string   `json:"use_case"`
		ExecutionGuidance    string   `json:"execution_guidance"`
		RecommendedPlanSteps []string `json:"recommended_plan_steps"`
		KnownPitfalls        []string `json:"known_pitfalls"`
		PrimaryToolSlugs     []string `json:"primary_tool_slugs"`
		RelatedToolSlugs     []string `json:"related_tool_slugs"`
		Toolkits             []string `json:"toolkits"`
		Error                *string  `json:"error"`
	} `json:"results"`
	Connections []struct {
		Toolkit             string `json:"toolkit"`
		Description         string `json:"description"`
		HasActiveConnection bool   `json:"has_active_connection"`
		StatusMessage       string `json:"status_message"`
		Accounts            []struct {
			ID        string `json:"id"`
			Alias     string `json:"alias"`
			Status    string `json:"status"`
			IsDefault bool   `json:"is_default"`
		} `json:"accounts"`
	} `json:"toolkit_connection_statuses"`
	ToolSchemas map[string]struct {
		Toolkit       string          `json:"toolkit"`
		Description   string          `json:"description"`
		InputSchema   json.RawMessage `json:"input_schema"`
		HasFullSchema bool            `json:"hasFullSchema"`
	} `json:"tool_schemas"`
}

// Search finds tools for natural-language use cases across every toolkit.
func (c *Client) Search(ctx context.Context, session string, useCases []string) (SearchResult, error) {
	queries := make([]map[string]string, 0, len(useCases))
	for _, useCase := range useCases {
		queries = append(queries, map[string]string{"use_case": useCase})
	}
	var out SearchResult
	if err := c.do(ctx, http.MethodPost, sessionPath(session, "search"), nil, map[string]any{"queries": queries}, &out); err != nil {
		return SearchResult{}, err
	}
	if !out.Success && out.Error != nil && strings.TrimSpace(*out.Error) != "" {
		return SearchResult{}, &APIError{Status: http.StatusUnprocessableEntity, Message: truncate(*out.Error, 400)}
	}
	return out, nil
}

// ToolSchemas returns full input schemas for exact tool slugs.
func (c *Client) ToolSchemas(ctx context.Context, session string, slugs []string) (json.RawMessage, error) {
	var out struct {
		Data  json.RawMessage `json:"data"`
		Error *string         `json:"error"`
	}
	err := c.do(ctx, http.MethodPost, sessionPath(session, "execute_meta"), nil, map[string]any{
		"slug": "COMPOSIO_GET_TOOL_SCHEMAS", "arguments": map[string]any{"tool_slugs": slugs, "include": []string{"input_schema"}},
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Error != nil && strings.TrimSpace(*out.Error) != "" {
		return nil, &APIError{Status: http.StatusUnprocessableEntity, Message: truncate(*out.Error, 400)}
	}
	return out.Data, nil
}

// Execution is one tool run. A non-empty Error is the provider's answer.
type Execution struct {
	Data  json.RawMessage `json:"data"`
	Error *string         `json:"error"`
	LogID string          `json:"log_id"`
}

// Execute runs one tool in the session. Account selects among several
// connected accounts by ID or alias; empty uses the only one.
func (c *Client) Execute(ctx context.Context, session, slug string, arguments json.RawMessage, account string) (Execution, error) {
	body := map[string]any{"tool_slug": slug, "arguments": arguments}
	if account != "" {
		body["account"] = account
	}
	var out Execution
	err := c.do(ctx, http.MethodPost, sessionPath(session, "execute"), nil, body, &out)
	return out, err
}

// Link starts sign-in for one toolkit and returns Composio's Connect Link.
func (c *Client) Link(ctx context.Context, session, toolkit string) (string, error) {
	var out struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := c.do(ctx, http.MethodPost, sessionPath(session, "link"), nil, map[string]any{"toolkit": toolkit}, &out); err != nil {
		return "", err
	}
	if !connectLinkAllowed(out.RedirectURL) {
		return "", &APIError{Status: http.StatusBadGateway, Message: "Composio returned a sign-in link outside composio.dev."}
	}
	return out.RedirectURL, nil
}

// connectLinkAllowed keeps sign-in links on Composio's own HTTPS domain.
func connectLinkAllowed(raw string) bool {
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || link.User != nil || link.Opaque != "" || link.Port() != "" {
		return false
	}
	host := strings.ToLower(link.Hostname())
	return host == "composio.dev" || strings.HasSuffix(host, ".composio.dev")
}

// Toolkit is one app in the catalog, with the session's connection to it.
type Toolkit struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	NoAuth bool   `json:"is_no_auth"`
	Meta   struct {
		Description string `json:"description"`
	} `json:"meta"`
	ConnectedAccount json.RawMessage `json:"connected_account"`
}

func (t Toolkit) Connected() bool {
	value := strings.TrimSpace(string(t.ConnectedAccount))
	return value != "" && value != "null"
}

// Toolkits pages through the app catalog, optionally filtered by text or to
// the apps the user has connected.
func (c *Client) Toolkits(ctx context.Context, session, search, cursor string, connectedOnly bool) ([]Toolkit, string, error) {
	query := url.Values{"limit": {"30"}}
	if connectedOnly {
		query.Set("is_connected", "true")
		query.Set("limit", "50")
	}
	if search = strings.TrimSpace(search); search != "" {
		query.Set("search", search)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var out struct {
		Items      []Toolkit `json:"items"`
		NextCursor *string   `json:"next_cursor"`
	}
	if err := c.do(ctx, http.MethodGet, sessionPath(session, "toolkits"), query, nil, &out); err != nil {
		return nil, "", err
	}
	next := ""
	if out.NextCursor != nil {
		next = *out.NextCursor
	}
	return out.Items, next, nil
}
