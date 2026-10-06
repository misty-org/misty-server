package composio

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// identityTool is a read-only call that reveals who a connection signed in as,
// for apps whose connection carries no display name.
type identityTool struct {
	slug      string
	arguments string
	read      func(json.RawMessage) string
}

var identityTools = map[string]identityTool{
	"discord": {"DISCORD_GET_MY_USER", `{}`, func(data json.RawMessage) string {
		var user struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal(data, &user)
		return user.Username
	}},
	// Calendar is granted events only; the primary calendar's summary is the
	// owner's email address.
	"googlecalendar": {"GOOGLECALENDAR_EVENTS_LIST", `{"calendarId":"primary","maxResults":1}`, func(data json.RawMessage) string {
		var calendar struct {
			Summary string `json:"summary"`
		}
		_ = json.Unmarshal(data, &calendar)
		return calendar.Summary
	}},
}

var identityCache sync.Map // account ID -> identity

// Identities names the external account behind each connection, such as an
// email address or username, keyed by account ID. Accounts it cannot name are
// left out; a failed lookup is retried on the next call.
func (c *Client) Identities(ctx context.Context, session string, accounts []Account) map[string]string {
	names := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	set := func(id, name string) {
		mu.Lock()
		names[id] = name
		mu.Unlock()
	}
	for _, account := range accounts {
		if name := strings.TrimSpace(account.State.Val.DisplayName); name != "" {
			set(account.ID, truncate(name, 120))
			continue
		}
		if cached, ok := identityCache.Load(account.ID); ok {
			set(account.ID, cached.(string))
			continue
		}
		tool, ok := identityTools[account.Toolkit.Slug]
		if !ok || !account.Active() {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			out, err := c.Execute(ctx, session, tool.slug, json.RawMessage(tool.arguments), id)
			if err != nil || (out.Error != nil && strings.TrimSpace(*out.Error) != "") {
				return
			}
			name := truncate(strings.TrimSpace(tool.read(out.Data)), 120)
			if name == "" {
				return
			}
			identityCache.Store(id, name)
			set(id, name)
		}(account.ID)
	}
	wg.Wait()
	return names
}
