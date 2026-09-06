package api

import (
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// TestingAuthorizeAppRuntimeRequest is the server-side capability boundary for
// hosted apps. Unknown routes are denied by default, even when the token is
// otherwise valid.
func TestingAuthorizeAppRuntimeRequest(session db.AppRuntimeSession, method, path string) bool {
	path = unversionedAPIPath(path)
	if path == "/billing/usage" {
		return method == http.MethodGet && hasAppScope(session, "ai.read")
	}
	if path == "/app-runtime/session" {
		return method == http.MethodGet
	}
	if path == "/app-runtime/records" {
		return method == http.MethodGet && hasAppScope(session, "storage.read")
	}
	if strings.HasPrefix(path, "/app-runtime/records/") {
		return (method == http.MethodPut || method == http.MethodDelete) && hasAppScope(session, "storage.write")
	}
	if domain := accountAppRuntimeDomain(path); domain != "" {
		access := "write"
		if method == http.MethodGet || method == http.MethodHead {
			access = "read"
		}
		return hasAppScope(session, domain+"."+access)
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 1 && segments[0] == "spaces" {
		// Account-scoped apps may list memberships; Space-bound apps remain limited
		// to their own Space and must use its individual endpoint.
		return session.SpaceID == "" && method == http.MethodGet && hasAppScope(session, "spaces.read")
	}
	if len(segments) < 2 || segments[0] != "spaces" || segments[1] == "" {
		return false
	}
	if session.SpaceID == "" || segments[1] != session.SpaceID {
		return false
	}
	if len(segments) == 2 {
		return method == http.MethodGet && hasAppScope(session, "spaces.read")
	}
	resource := segments[2]
	if resource == "nodes" {
		return hasAppScope(session, "files.read") && (method == http.MethodGet || (method == http.MethodPost && len(segments) == 5 && segments[4] == "resolve"))
	}
	if resource == "calendar" || resource == "integrations" {
		return authorizeAppCalendarConnection(session, method, segments[2:])
	}
	if resource == "members" {
		return method == http.MethodGet && hasAppScope(session, "spaces.read")
	}
	domain := ""
	switch resource {
	case "home":
		domain = "spaces"
	case "agenda":
		domain = "tasks"
	case "roadmap-node-definitions":
		domain = "roadmaps"
	case "messages", "conversations", "social", "read", "action-suggestions", "action-suggestion-settings":
		domain = "messages"
	case "notes":
		domain = "notes"
	case "drawings":
		domain = "drawings"
	case "tasks":
		domain = "tasks"
	case "roadmaps":
		domain = "roadmaps"
	case "library", "attachments":
		if hasAppScope(session, "library.read") || hasAppScope(session, "library.write") {
			domain = "library"
		} else {
			domain = "files"
		}
	case "activity", "events":
		domain = "activity"
	case "agents", "studio":
		domain = "agents"
	default:
		return false
	}
	access := "write"
	if method == http.MethodGet || method == http.MethodHead {
		access = "read"
	}
	return hasAppScope(session, domain+"."+access)
}

// Enumerate these routes so granting calendar access cannot implicitly expose
// a future credential-management endpoint. The regular handlers still enforce
// membership and management permissions after this app-token ceiling.
func authorizeAppCalendarConnection(session db.AppRuntimeSession, method string, route []string) bool {
	if route[0] == "integrations" {
		if len(route) == 1 && method == http.MethodGet {
			return hasAppScope(session, "connections.read")
		}
		if len(route) == 3 && route[1] != "" {
			switch route[2] {
			case "resources":
				return (method == http.MethodGet && hasAppScope(session, "connections.read")) ||
					(method == http.MethodPut && hasAppScope(session, "connections.write"))
			case "authorize", "bind":
				return method == http.MethodPost && hasAppScope(session, "connections.write")
			}
		}
		return false
	}
	if len(route) == 2 {
		switch route[1] {
		case "events", "sources":
			return (method == http.MethodGet && hasAppScope(session, "calendar.read")) ||
				(method == http.MethodPost && hasAppScope(session, "calendar.write"))
		case "sync":
			return method == http.MethodPost && hasAppScope(session, "calendar.write") && hasAppScope(session, "tasks.write")
		}
	}
	if len(route) == 3 && route[2] != "" {
		switch route[1] {
		case "events":
			return (method == http.MethodPatch || method == http.MethodDelete) && hasAppScope(session, "calendar.write")
		case "sources":
			return method == http.MethodDelete && hasAppScope(session, "calendar.write")
		case "google":
			return route[2] == "calendars" && method == http.MethodGet && hasAppScope(session, "calendar.read") && hasAppScope(session, "connections.read")
		}
	}
	return false
}

func accountAppRuntimeDomain(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 0 {
		return ""
	}
	switch segments[0] {
	case "activity":
		return "activity"
	case "me":
		if len(segments) == 1 || (len(segments) == 2 && segments[1] == "avatar") {
			return "profile"
		}
	case "mail":
		return "mail"
	case "connections", "cloud":
		return "connections"
	case "agents", "agent-runs", "agent-voice", "runs":
		return "agents"
	case "mcp":
		return "mcp"
	case "automations":
		return "automations"
	case "devices":
		return "devices"
	case "search":
		return "search"
	case "ai":
		if len(segments) > 1 && segments[1] == "media-search" {
			return "media-search"
		}
		if len(segments) > 1 && segments[1] == "smart-library" {
			return "library"
		}
		return "ai"
	case "misty":
		return "ai"
	}
	return ""
}

func hasAppScope(session db.AppRuntimeSession, expected string) bool {
	for _, scope := range session.Scopes {
		if scope == expected {
			return true
		}
	}
	return false
}

func unversionedAPIPath(path string) string {
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	if strings.HasPrefix(path, "/api/") {
		return strings.TrimPrefix(path, "/api")
	}
	if strings.HasPrefix(path, "/v1/") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}
