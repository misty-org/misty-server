package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var readOnlyPosts = []*regexp.Regexp{
	regexp.MustCompile(`^/(login|register|logout|setup-keys/redeem)$`),
	regexp.MustCompile(`^/auth/(refresh|google|google/complete|confirm-email|confirm-email/sign-in|web-handoff)$`),
	regexp.MustCompile(`^/me/(export|deletion|account-proof|web-handoff)$`),
	regexp.MustCompile(`^/realtime/tickets?$`),
	regexp.MustCompile(`^/spaces/[^/]+/(notes|code-sessions)/[^/]+/collaboration-ticket$`),
	regexp.MustCompile(`^/spaces/[^/]+/drive/items/[^/]+/collaboration-ticket$`),
	regexp.MustCompile(`^/spaces/[^/]+/apps/[^/]+/rooms/[^/]+/collaboration-ticket$`),
	regexp.MustCompile(`^/spaces/[^/]+/nodes/[^/]+/resolve$`),
	regexp.MustCompile(`^/spaces/[^/]+/(conversations/[^/]+/)?read$`),
}

func readOnlyRequest(r *http.Request) bool {
	path := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/v1")
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPut:
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

func collaborationRole(userID, role string) string {
	if _, closing := suspensions.Closing(context.Background(), userID); closing {
		return "viewer"
	}
	return role
}
