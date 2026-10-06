package composio

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

var accountPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

// Account is a connected app account. Only the provider's display name is
// read from Composio's `state` field; the credentials beside it are never decoded.
type Account struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Status    string `json:"status"`
	Alias     string `json:"alias"`
	Disabled  bool   `json:"is_disabled"`
	CreatedAt string `json:"created_at"`
	Toolkit   struct {
		Slug string `json:"slug"`
	} `json:"toolkit"`
	State struct {
		Val struct {
			DisplayName string `json:"displayName"`
		} `json:"val"`
	} `json:"state"`
}

func (a Account) Active() bool { return a.Status == "ACTIVE" && !a.Disabled }

// Accounts lists one user's connected accounts, optionally for one toolkit.
// Items for any other user are dropped even if Composio returns them.
func (c *Client) Accounts(ctx context.Context, userID, toolkit string) ([]Account, error) {
	query := url.Values{"user_ids": {userID}, "limit": {"100"}}
	if toolkit != "" {
		query.Set("toolkit_slugs", toolkit)
	}
	items := []Account{}
	for page := 0; page < 5; page++ {
		var out struct {
			Items      []Account `json:"items"`
			NextCursor *string   `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "connected_accounts", query, nil, &out); err != nil {
			return nil, err
		}
		for _, item := range out.Items {
			if item.UserID == userID && (toolkit == "" || item.Toolkit.Slug == toolkit) {
				items = append(items, item)
			}
		}
		if out.NextCursor == nil || *out.NextCursor == "" {
			break
		}
		query.Set("cursor", *out.NextCursor)
	}
	return items, nil
}

// DeleteAccount removes a connected account after confirming its owner.
func (c *Client) DeleteAccount(ctx context.Context, userID, id string) error {
	if !accountPattern.MatchString(id) {
		return &APIError{Status: http.StatusNotFound, Message: "That connection does not exist."}
	}
	var account Account
	if err := c.do(ctx, http.MethodGet, "connected_accounts/"+url.PathEscape(id), nil, nil, &account); err != nil {
		return err
	}
	if account.ID != id || account.UserID != userID {
		return &APIError{Status: http.StatusNotFound, Message: "That connection does not exist."}
	}
	return c.do(ctx, http.MethodDelete, "connected_accounts/"+url.PathEscape(id), nil, nil, nil)
}

// ToolInfo is a tool's identity and Composio's behavior tags.
type ToolInfo struct {
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	Toolkit struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"toolkit"`
}

var toolCache sync.Map // slug -> cachedTool

type cachedTool struct {
	info    ToolInfo
	expires time.Time
}

// Tool returns a tool's tags, cached briefly per process.
func (c *Client) Tool(ctx context.Context, slug string) (ToolInfo, error) {
	if !ValidToolSlug(slug) {
		return ToolInfo{}, &APIError{Status: http.StatusNotFound, Message: "Unknown tool slug."}
	}
	if cached, ok := toolCache.Load(slug); ok && time.Now().Before(cached.(cachedTool).expires) {
		return cached.(cachedTool).info, nil
	}
	var info ToolInfo
	if err := c.do(ctx, http.MethodGet, "tools/"+url.PathEscape(slug), nil, nil, &info); err != nil {
		return ToolInfo{}, err
	}
	if info.Slug != slug {
		return ToolInfo{}, &APIError{Status: http.StatusNotFound, Message: "Unknown tool slug."}
	}
	toolCache.Store(slug, cachedTool{info: info, expires: time.Now().Add(30 * time.Minute)})
	return info, nil
}
