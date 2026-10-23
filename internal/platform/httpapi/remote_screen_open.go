package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// remoteScreenOpen handles screen_open with a browser session on the sending
// computer, unless the agent asked for the person's own page or other apps.
// The computer opens the agent's own tab, and the tab joins this run, so the
// agent keeps working in the same run with browser tools from its next step
// instead of handing off to a new run. It returns nil, nil when no session applies.
func (s *SpacesService) remoteScreenOpen(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, callID, url, reason string, desktop bool, place string) (json.RawMessage, error) {
	// Calls made together (one per site) take turns, so each sees the tabs the one before
	// it opened and gets its own instead of racing for the first.
	lock := &screenOpenLocks[fnv32(record.ID)%uint32(len(screenOpenLocks))]
	lock.Lock()
	defer lock.Unlock()
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	// Only acting in the person's own page (current_tab) or other apps (desktop) hands off;
	// a new tab or window becomes a tab of the agent's own, in this run.
	if place == "current_tab" || desktop {
		return nil, nil
	}
	target := sessionTarget(contexts)
	if target == nil {
		return nil, nil
	}
	name := "this computer"
	programs := contextAllows(target, browserRunTool)
	// The agent's own tabs on that device in this run. A call with a url opens another
	// one beside them (up to maxOwnTabs), so pages the person asked to keep open stay
	// open; a call without one goes back to the first.
	own := []string{}
	already := ""
	for _, item := range contexts {
		var metadata map[string]any
		if item.Kind == "browser_tab" && item.State == "attached" && item.DeviceID == target.DeviceID && json.Unmarshal(item.Metadata, &metadata) == nil && metadata["remote_session"] == true {
			own = append(own, item.OpaqueRef)
			// A page opened again goes back to its tab rather than a duplicate of it.
			if url != "" && metadata["opened_url"] == url && already == "" {
				already = item.OpaqueRef
			}
		}
	}
	if already != "" {
		return sessionOpened(already, name, url, true, programs, own...)
	}
	if len(own) > 0 && (url == "" || len(own) >= maxOwnTabs) {
		return sessionOpened(own[0], name, url, true, programs, own...)
	}
	remote, err := s.openRemoteSession(ctx, record, body, target, callID, url, strings.TrimSpace(reason), len(own) > 0)
	if err != nil {
		return nil, err
	}
	tab := remote.Contexts[0]
	// The tab works for the window that asked, as a screen the person attached would.
	var metadata map[string]any
	if json.Unmarshal(tab.Metadata, &metadata) != nil {
		return nil, deviceOfflineError{name: name, reason: "returned an invalid browser session."}
	}
	metadata["window_label"] = body.WindowLabel
	if url != "" {
		metadata["opened_url"] = url
	}
	tab.Metadata, _ = json.Marshal(metadata)
	if _, err := s.database.AttachRunningAIInvocationContext(ctx, record.UserID, record.ID, tab.DeviceID, tab.Kind, tab.OpaqueRef, tab.DisplayName, tab.Capabilities, tab.Metadata, runGrantRecord(tab.RunGrant)); err != nil {
		// Calls made together (one per site) share the device's one session tab: the
		// first attaches it and the others find it attached.
		if attached, readErr := s.database.AIInvocationContexts(ctx, record.UserID, record.ID); readErr == nil {
			for _, item := range attached {
				if item.OpaqueRef == tab.OpaqueRef {
					return sessionOpened(tab.OpaqueRef, name, url, true, programs, append(own, tab.OpaqueRef)...)
				}
			}
		}
		return nil, err
	}
	return sessionOpened(tab.OpaqueRef, name, url, false, programs, append(own, tab.OpaqueRef)...)
}

// screenOpenLocks serialize screen_open per run (striped, so nothing accumulates).
var screenOpenLocks [64]sync.Mutex

func fnv32(text string) uint32 {
	hash := uint32(2166136261)
	for index := 0; index < len(text); index++ {
		hash = (hash ^ uint32(text[index])) * 16777619
	}
	return hash
}

// maxOwnTabs is how many tabs of its own a run may keep open on one device.
const maxOwnTabs = 32

// sessionOpened tells the agent which of its own tabs it is in, and lists every one.
func sessionOpened(scopeID, name, url string, reused, programs bool, tabs ...string) (json.RawMessage, error) {
	opened := "Your own browser tab is open on " + name
	if reused {
		opened = "Your own browser tab on " + name + " is already open"
	}
	// The runtime adds the new page's snapshot right after this result (agent-runtime src/opened-pages.ts).
	next := "Its page snapshot follows, so act on it next"
	if url == "" || reused {
		next = "Go to pages in it with browser_navigate, which returns each page's snapshot"
	}
	if len(tabs) == 0 {
		tabs = []string{scopeID}
	}
	if programs {
		// With browser_run the page is worked in programs (docs/agents/harness.md, browser_run): no
		// snapshot follows, and the message points there rather than at the step tools.
		return json.Marshal(map[string]any{"status": "open", "scopeId": scopeID, "tabs": tabs, "catalog_changed": true,
			"message": opened + " (scopeId " + scopeID + "). Work it with browser_run: getTab(\"" + scopeID + "\") gives this tab in a program, openTab(url) opens another. " +
				"Your open tabs: " + strings.Join(tabs, ", ") + "."})
	}
	return json.Marshal(map[string]any{"status": "open", "scopeId": scopeID, "tabs": tabs, "catalog_changed": true,
		"message": opened + " (scopeId " + scopeID + "). Browser tools act on it from your next step; pass its scopeId. " + next + ", and keep working in this run. " +
			"Your open tabs: " + strings.Join(tabs, ", ") + ". Open a new tab (screen_open with its url, up to 32) only for a page the person wants kept open; read other pages by navigating a tab you have."})
}
