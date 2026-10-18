package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Screens open on demand. A run started on the desktop can ask for one with
// screen.open (a browser, or the user's other apps with Misty's own cursor),
// or see the user's screen with screen.look. Either ends the run
// with a screen request; the desktop opens the screen where the account
// setting says and continues the same conversation with it attached, through
// the same lease and admission checks as any other screen task.
const (
	screenOpenTool = "screen.open"
	screenLookTool = "screen.look"
)

// screenRequest is what the desktop needs to open a screen and continue.
type screenRequest struct {
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Reason   string `json:"reason"`
	Location string `json:"location"`
	// Chosen reports that the user's words named the place; otherwise the
	// desktop reuses an open tab that matches before using Location.
	Chosen bool `json:"chosen,omitempty"`
	// Remote is a browser session another device opened for this request.
	Remote *remoteScreen `json:"remote,omitempty"`
}

// screenToolRegistrations are offered to desktop runs only; other surfaces
// have no screen to open.
func (s *SpacesService) screenToolRegistrations(record *db.AIInvocationRecord) []agenttools.Registration {
	var body aiInvocationInput
	if record == nil || json.Unmarshal(record.RequestPayload, &body) != nil || body.WindowLabel == "" || body.AgentID == "" {
		return nil
	}
	reason := map[string]any{"type": "string", "minLength": 3, "maxLength": 300, "description": "What the screen is for, in a few words the user will see"}
	return []agenttools.Registration{
		{Descriptor: screenDescriptor(screenOpenTool,
			"Open a screen for this task. Pick the target from the user's words: current_tab when they mean the page in front of them "+
				"(\"this page\", \"this tab\", \"here\"); new_tab for a website in a new tab beside their work (\"in a new tab\"); window for "+
				"a separate Misty window that leaves theirs alone (\"in a new window\", \"in the background\", \"on your own\"); desktop "+
				"for other apps on their Mac or the whole screen, such as Numbers, Finder or Mail, where Misty works with its own cursor "+
				"after they allow it. Omit target when they did not say, or when they mean a site they already have open (\"my Gmail tab\"), "+
				"and pass that site's url: Misty reuses an open tab that matches, otherwise opens one where they prefer. "+
				"If such a screen is already attached, use browser_act with its scopeId instead. With no target, and a url when you know "+
				"the page, this opens your own browser tab in the background and keeps this run going: browser tools act on it from "+
				"your next step. A named target ends the current response; Misty opens that screen and continues this conversation "+
				"with it attached. Do not ask the user where to work.",
			map[string]any{"reason": reason,
				"target": map[string]any{"type": "string", "enum": []string{"current_tab", "new_tab", "window", "desktop"}, "description": "Where to work. Omit for the user's preferred place for new screens."},
				"url":    map[string]any{"type": "string", "maxLength": 2048, "description": "Optional https page to open first (new_tab or window)"}}, "reason"),
			Handler: s.screenRequestHandler(screenOpenTool)},
		{Descriptor: screenDescriptor(screenLookTool,
			"See what is on the user's screen right now, when the request refers to something visible there. "+
				"Ends the current response; Misty captures the screen and continues this conversation with the image attached.",
			map[string]any{"reason": reason}, "reason"),
			Handler: s.screenRequestHandler(screenLookTool)},
	}
}

func screenDescriptor(name, description string, properties map[string]any, required ...string) agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: name, Version: 1, Description: description, Risk: serveragent.RiskRead,
		InputSchema:  TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}),
		OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalNone, Layer: agenttools.LayerPrivate, Locality: agenttools.LocalityServer,
		Idempotent: true, AllowCustomAgent: true, AuditEvent: name,
	}
}

func (s *SpacesService) screenRequestHandler(tool string) agenttools.Handler {
	return func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
		var input struct {
			Reason string `json:"reason"`
			URL    string `json:"url"`
			Target string `json:"target"`
		}
		if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Reason) == "" {
			return nil, serveragent.ErrInvalidRequest("Describe what the screen is for.")
		}
		if input.URL != "" && !strings.HasPrefix(input.URL, "https://") && !strings.HasPrefix(input.URL, "http://") {
			return nil, serveragent.ErrInvalidRequest("url must be an http or https address.")
		}
		record, err := s.database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
		if err != nil {
			return nil, err
		}
		var body aiInvocationInput
		if json.Unmarshal(record.RequestPayload, &body) != nil {
			return nil, db.ErrSpaceInvalid
		}
		kind := strings.TrimPrefix(tool, "screen.")
		if tool == screenOpenTool {
			// Earlier runs named the default "browser" and the current tab "tab".
			switch input.Target {
			case "browser":
				input.Target = ""
			case "tab":
				input.Target = "current_tab"
			}
			desktop := input.Target == "desktop"
			switch input.Target {
			case "desktop":
				kind = "desktop"
				input.URL = ""
			case "current_tab":
				input.URL = ""
			case "", "new_tab", "window":
			default:
				return nil, serveragent.ErrInvalidRequest("target must be current_tab, new_tab, window or desktop.")
			}
			if remote, err := s.remoteScreenOpen(ctx, record, body, request.ID, input.URL, input.Reason, desktop, input.Target); remote != nil || err != nil {
				return remote, err
			}
			if invocationHasDesktopControl(body) == desktop {
				// Folders and the tab list are attached to most runs; only a scope
				// browser_act can drive counts as an open screen.
				if screens, capabilities := aiInvocationBrowserGrants(ctx, s.database, invocation.UserID, invocation.RunID); capabilities[screenActTool] {
					return json.Marshal(map[string]any{"status": "open", "screens": screens,
						"message": "That screen is already attached to this task. Use browser_act with its scopeId."})
				}
			}
		} else if len(body.DisplayCaptures) > 0 || body.Capture != nil {
			return json.Marshal(map[string]any{"status": "open",
				"message": "The user's screen is already attached to this request. Read it from the attached image."})
		}
		settings, _, err := s.database.AISettings(ctx, invocation.UserID)
		if err != nil {
			return nil, err
		}
		screen := &screenRequest{Kind: kind, URL: input.URL, Reason: strings.TrimSpace(input.Reason), Location: settings.ScreenLocation, Chosen: input.Target != ""}
		// The user's words override where new screens usually open. Locations:
		// current (their tab), window (a new tab in their window), separate.
		switch input.Target {
		case "current_tab":
			screen.Location = "current"
		case "new_tab":
			screen.Location = "window"
		case "window":
			screen.Location = "separate"
		}
		if s.aiInvocations != nil {
			if _, err := s.aiInvocations.restoreDurable(ctx, *record); err != nil {
				return nil, err
			}
			if err := s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "screen.request", ScreenRequest: screen}); err != nil {
				return nil, err
			}
		}
		return json.Marshal(map[string]any{"status": "screen_requested", "kind": screen.Kind,
			"message": "Misty is opening the screen and will continue this conversation with it attached."})
	}
}
