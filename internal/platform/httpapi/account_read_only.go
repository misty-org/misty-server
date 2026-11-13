package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// A closing account (POST /operator/v1/accounts/{id}/close) is read-only until
// it is deleted: it signs in, refreshes, reads everything and exports, and
// every change answers 403 account_closing with the deletion date. The guard
// decides by method: GET, HEAD and OPTIONS read, and so do the few POST routes
// listed here and PUT /me/password. Writes that arrive another way are refused where they land:
// agent runs in the database (db.ErrAccountClosing), agent
// tool calls in the MCP server, and note and drawing rooms through a viewer
// ticket.

// readOnlyPosts are the POST routes a closing account may still use, after any
// /v1 or /api prefix: signing in and out (a setup key's sign-in link
// included), refreshing, exporting or deleting
// the account, opening the website signed in (to subscribe again), the tickets
// that open live updates, collaboration rooms and Space files for
// reading, read receipts, and a device's presence.
var readOnlyPosts = []*regexp.Regexp{
	regexp.MustCompile(`^/(login|register|logout|setup-keys/redeem)$`),
	regexp.MustCompile(`^/auth/(refresh|handoff|google|google/complete|confirm-email|confirm-email/sign-in)$`),
	regexp.MustCompile(`^/me/(export|deletion|account-proof)$`),
	regexp.MustCompile(`^/realtime/tickets?$`),
	regexp.MustCompile(`^/spaces/[^/]+/(notes|drawings)/[^/]+/collaboration-ticket$`),
	regexp.MustCompile(`^/spaces/[^/]+/nodes/[^/]+/resolve$`),
	regexp.MustCompile(`^/spaces/[^/]+/(conversations/[^/]+/)?read$`),
	regexp.MustCompile(`^/devices/[^/]+/(heartbeat|channel-ticket)$`),
}

// readOnlyRequest reports whether a closing account may make this request.
func readOnlyRequest(r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	for _, prefix := range []string{"/v1", "/api"} {
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPut:
		// Choosing a password, after a setup key's sign-in link too: a closing
		// customer who forgot theirs still signs in and exports. The server sends
		// no email, so a sign-in link is the only reset.
		return path == "/me/password"
	case http.MethodPost:
	default:
		return false
	}
	for _, route := range readOnlyPosts {
		if route.MatchString(path) {
			return true
		}
	}
	return false
}

// AccountClosingMessage says the account is read-only, until when, and how to
// keep it.
func AccountClosingMessage(deleteAt time.Time) string {
	return "This account is closing and is read-only. You can still sign in, open everything and export it until " +
		deleteAt.UTC().Format("January 2, 2006") + " (UTC), when it is deleted. Subscribe again before then to keep it."
}

func writeAccountClosing(w http.ResponseWriter, deleteAt time.Time) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"code":      "account_closing",
		"message":   AccountClosingMessage(deleteAt),
		"delete_at": deleteAt.UTC().Format(time.RFC3339),
	})
}

// collaborationRole is the role a collaboration ticket carries: a closing
// account opens notes and drawings as a viewer, so their rooms take no edits.
func collaborationRole(userID, role string) string {
	if _, closing := suspensions.Closing(context.Background(), userID); closing {
		return "viewer"
	}
	return role
}
